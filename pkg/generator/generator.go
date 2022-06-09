/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package generator

import (
	"fmt"
	"io"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/gengo/generator"
	"k8s.io/gengo/namer"
	"k8s.io/gengo/types"
	"k8s.io/klog/v2"
)

type Generator struct {
	generator.DefaultGen

	Options *Options

	// ImportTracker tracks the raw namer's imports.
	// It can be re-used by wrapper generators.
	ImportTracker namer.ImportTracker

	/* Internal state */

	// typesPackage is the package that contains the types that conversion func are going to be
	// generated for.
	typesPackage *types.Package
	// outputPackage is the package that the conversion funcs are going to be output to.
	outputPackage *types.Package
	// peerPackages are the packages that contain the peers of typesPackage's types .
	peerPackages []string
	// unsafeConversionArbitrator allows comparing types' memory layouts to decide whether
	// to use unsafe conversions.
	unsafeConversionArbitrator *unsafeConversionArbitrator
	// peerTypes caches the peer types found so far.
	peerTypes map[string]*types.Type
}

// NewConversionGenerator builds a new Generator.
func NewConversionGenerator(context *generator.Context, outputFileName, typesPackage, outputPackage string, peerPackages []string, options *Options) (*Generator, error) {
	if options == nil {
		options = DefaultOptions()
	}
	if options.ManualConversionsTracker == nil {
		options.ManualConversionsTracker = NewManualConversionsTracker()
	}

	typesPkg, err := getPackage(context, typesPackage)
	if err != nil {
		return nil, err
	}
	oututPkg, err := getPackage(context, outputPackage)
	if err != nil {
		return nil, err
	}

	g := &Generator{
		DefaultGen: generator.DefaultGen{
			OptionalName: outputFileName,
		},
		Options: options,

		ImportTracker: generator.NewImportTracker(),

		typesPackage:  typesPkg,
		outputPackage: oututPkg,

		unsafeConversionArbitrator: newUnsafeConversionArbitrator(options.ManualConversionsTracker),
		peerTypes:                  make(map[string]*types.Type),
	}

	// get peer packages from the package's doc.go file, if any
	g.peerPackages = append(g.extractDocFileTag(options.PeerPackagesTagName), peerPackages...)

	if err := findManualConversionFunctions(context, options.ManualConversionsTracker,
		append(g.peerPackages, outputPackage, typesPackage)); err != nil {
		return nil, err
	}

	return g, nil
}

// TODO wkpo need to be quite that verbose?
func getPackage(context *generator.Context, pkgPath string) (*types.Package, error) {
	pkg := context.Universe[pkgPath]
	if pkg != nil {
		return pkg, nil
	}
	pkg, err := context.AddDirectory(pkgPath)
	return pkg, errors.Wrapf(err, "unable to load package %q", pkgPath)
}

func findManualConversionFunctions(context *generator.Context, tracker *ManualConversionsTracker, packagePaths []string) error {
	for _, packagePath := range packagePaths {
		if errors := tracker.findManualConversionFunctions(context, packagePath); len(errors) != 0 {
			errMsg := "Errors when looking for manual conversion functions in " + packagePath + ":"
			for _, err := range errors {
				errMsg += "\n" + err.Error()
			}
			return fmt.Errorf(errMsg)
		}
	}
	return nil
}

// The names of the namers used by ConversionGenerators.
// They're chosen to hopefully not conflict with namers from wrapper generators.
const (
	rawNamer                  = "ConversionGenerator_raw"
	publicImportTrackingNamer = "ConversionGenerator_publicIT"
)

// Namers returns the name system used by ConversionGenerators.
func (g *Generator) Namers(*generator.Context) namer.NameSystems {
	return namer.NameSystems{
		rawNamer: namer.NewRawNamer(g.outputPackage.Path, g.ImportTracker),
		publicImportTrackingNamer: &namerPlusImportTracking{
			delegate: ConversionNamer(),
			tracker:  g.ImportTracker,
		},
	}
}

type namerPlusImportTracking struct {
	delegate namer.Namer
	tracker  namer.ImportTracker
}

func (n *namerPlusImportTracking) Name(t *types.Type) string {
	n.tracker.AddType(t)
	return n.delegate.Name(t)
}

// Filter filters the types this generator operates on.
func (g *Generator) Filter(context *generator.Context, t *types.Type) bool {
	peerType := g.GetPeerTypeFor(context, t)
	return peerType != nil && g.convertibleOnlyWithinPackage(t, peerType)
}

// Imports returns the imports to add to generated files.
func (g *Generator) Imports(*generator.Context) (imports []string) {
	// from the import tracker
	for _, importLine := range g.ImportTracker.ImportLines() {
		if g.isOtherPackage(importLine) {
			imports = append(imports, importLine)
		}
	}

	// from doc.go comments, if any
	for _, importLine := range g.extractDocFileTag(g.Options.ExtraImportsTagName) {
		imports = append(imports, importLine)
	}

	return
}

func (g *Generator) isOtherPackage(pkg string) bool {
	if pkg == g.outputPackage.Path {
		return false
	}
	if strings.HasSuffix(pkg, `"`+g.outputPackage.Path+`"`) {
		return false
	}
	return true
}

// GenerateType processes the given type.
func (g *Generator) GenerateType(context *generator.Context, t *types.Type, writer io.Writer) error {
	klog.V(5).Infof("generating for type %v", t)
	peerType := g.GetPeerTypeFor(context, t)
	sw := generator.NewSnippetWriter(writer, context, snippetDelimiter, snippetDelimiter)
	g.generateConversion(t, peerType, sw)
	g.generateConversion(peerType, t, sw)
	return sw.Error()

}

func (g *Generator) generateConversion(inType, outType *types.Type, sw *generator.SnippetWriter) {
	// function signature
	sw.Do("func auto", nil)
	g.writeConversionFunctionSignature(inType, outType, sw, true)
	sw.Do(" {\n", nil)

	// body
	errors := g.generateFor(inType, outType, sw)

	// close function body
	sw.Do("return nil\n", nil)
	sw.Do("}\n\n", nil)

	if _, found := g.preexists(inType, outType); found {
		// there is a public manual Conversion method: use it.
		return
	}

	if g.noPublicFun(inType) || g.noPublicFun(outType) {
		// no public conversion function
		return
	}

	if len(errors) == 0 {
		// Emit a public conversion function.
		sw.Do("// "+conversionFunctionNameTemplate(publicImportTrackingNamer)+" is an autogenerated conversion function.\nfunc ", argsFromType(inType, outType))
		g.writeConversionFunctionSignature(inType, outType, sw, true)
		sw.Do(" {\nreturn auto", nil)
		g.writeConversionFunctionSignature(inType, outType, sw, false)
		sw.Do("\n}\n\n", nil)
		return
	}

	// there were errors generating the private conversion function
	klog.Errorf("Warning: could not find nor generate a final Conversion function for %v -> %v", inType, outType)
	klog.Errorf("  you need to add manual conversions:")
	for _, err := range errors {
		klog.Errorf("      - %v", err)
	}
}

// writeConversionFunctionSignature writes the signature of the conversion function from inType to outType
// into the given snippet writer.
// includeArgsTypes controls whether the arguments' types' will be included.
func (g *Generator) writeConversionFunctionSignature(inType, outType *types.Type, sw *generator.SnippetWriter, includeArgsTypes bool) {
	args := argsFromType(inType, outType)
	sw.Do(conversionFunctionNameTemplate(publicImportTrackingNamer), args)
	sw.Do("(in", nil)
	if includeArgsTypes {
		sw.Do(" *$.inType|"+rawNamer+"$", args)
	}
	sw.Do(", out", nil)
	if includeArgsTypes {
		sw.Do(" *$.outType|"+rawNamer+"$", args)
	}
	for _, namedArgument := range g.Options.ManualConversionsTracker.additionalConversionArguments {
		sw.Do(fmt.Sprintf(", %s", namedArgument.Name), nil)
		if includeArgsTypes {
			sw.Do(" $.|"+rawNamer+"$", namedArgument.Type)
		}
	}
	sw.Do(")", nil)
	if includeArgsTypes {
		sw.Do(" error", nil)
	}
}

// we use the system of shadowing 'in' and 'out' so that the same code is valid
// at any nesting level. This makes the autogenerator easy to understand, and
// the compiler shouldn't care.
func (g *Generator) generateFor(inType, outType *types.Type, sw *generator.SnippetWriter) []error {
	klog.V(5).Infof("generating %v -> %v", inType, outType)
	var f func(*types.Type, *types.Type, *generator.SnippetWriter) []error

	switch inType.Kind {
	case types.Builtin:
		f = g.doBuiltin
	case types.Map:
		f = g.doMap
	case types.Slice:
		f = g.doSlice
	case types.Struct:
		f = g.doStruct
	case types.Pointer:
		f = g.doPointer
	case types.Alias:
		f = g.doAlias
	default:
		f = g.doUnknown
	}

	return f(inType, outType, sw)
}

func (g *Generator) doBuiltin(inType, outType *types.Type, sw *generator.SnippetWriter) []error {
	if inType == outType {
		sw.Do("*out = *in\n", nil)
	} else {
		sw.Do("*out = $.|"+rawNamer+"$(*in)\n", outType)
	}
	return nil
}

func (g *Generator) doMap(inType, outType *types.Type, sw *generator.SnippetWriter) (errors []error) {
	sw.Do("*out = make($.|"+rawNamer+"$, len(*in))\n", outType)
	if isDirectlyAssignable(inType.Key, outType.Key) {
		sw.Do("for key, val := range *in {\n", nil)
		if isDirectlyAssignable(inType.Elem, outType.Elem) {
			if inType.Key == outType.Key {
				sw.Do("(*out)[key] = ", nil)
			} else {
				sw.Do("(*out)[$.|"+rawNamer+"$(key)] = ", outType.Key)
			}
			if inType.Elem == outType.Elem {
				sw.Do("val\n", nil)
			} else {
				sw.Do("$.|"+rawNamer+"$(val)\n", outType.Elem)
			}
		} else {
			sw.Do("newVal := new($.|"+rawNamer+"$)\n", outType.Elem)

			manualOrInternal := false

			if function, ok := g.preexists(inType.Elem, outType.Elem); ok {
				manualOrInternal = true
				sw.Do("if err := $.|"+rawNamer+"$(&val, newVal"+g.extraArgumentsString()+"); err != nil {\n", function)
			} else if g.convertibleOnlyWithinPackage(inType.Elem, outType.Elem) {
				manualOrInternal = true
				sw.Do("if err := "+conversionFunctionNameTemplate(publicImportTrackingNamer)+"(&val, newVal"+g.extraArgumentsString()+"); err != nil {\n",
					argsFromType(inType.Elem, outType.Elem))
			}

			if manualOrInternal {
				sw.Do("return err\n}\n", nil)
			} else if g.Options.ExternalConversionsHandler == nil {
				klog.Warningf("%s's values of type %s require manual conversion to external type %s",
					inType.Name, inType.Elem, outType.Name)
			} else if _, err := g.Options.ExternalConversionsHandler(NewNamedVariable("&val", inType.Elem), NewNamedVariable("newVal", outType.Elem), sw); err != nil {
				errors = append(errors, err)
			}

			if inType.Key == outType.Key {
				sw.Do("(*out)[key] = *newVal\n", nil)
			} else {
				sw.Do("(*out)[$.|"+rawNamer+"$(key)] = *newVal\n", outType.Key)
			}
		}
	} else {
		// TODO: Implement it when necessary.
		sw.Do("for range *in {\n", nil)
		sw.Do("// FIXME: Converting unassignable keys unsupported $.|"+rawNamer+"$\n", inType.Key)
	}
	sw.Do("}\n", nil)

	return
}

func (g *Generator) doSlice(inType, outType *types.Type, sw *generator.SnippetWriter) (errors []error) {
	sw.Do("*out = make($.|"+rawNamer+"$, len(*in))\n", outType)
	if inType.Elem == outType.Elem && inType.Elem.Kind == types.Builtin {
		sw.Do("copy(*out, *in)\n", nil)
	} else {
		sw.Do("for i := range *in {\n", nil)
		if isDirectlyAssignable(inType.Elem, outType.Elem) {
			if inType.Elem == outType.Elem {
				sw.Do("(*out)[i] = (*in)[i]\n", nil)
			} else {
				sw.Do("(*out)[i] = $.|"+rawNamer+"$((*in)[i])\n", outType.Elem)
			}
		} else {
			manualOrInternal := false

			if function, ok := g.preexists(inType.Elem, outType.Elem); ok {
				manualOrInternal = true
				sw.Do("if err := $.|"+rawNamer+"$(&(*in)[i], &(*out)[i]"+g.extraArgumentsString()+"); err != nil {\n", function)
			} else if g.convertibleOnlyWithinPackage(inType.Elem, outType.Elem) {
				manualOrInternal = true
				sw.Do("if err := "+conversionFunctionNameTemplate(publicImportTrackingNamer)+"(&(*in)[i], &(*out)[i]"+g.extraArgumentsString()+"); err != nil {\n",
					argsFromType(inType.Elem, outType.Elem))
			}

			if manualOrInternal {
				sw.Do("return err\n}\n", nil)
			} else {
				conversionHandled := false
				var err error

				if g.Options.ExternalConversionsHandler == nil {
					klog.Warningf("%s's items of type %s require manual conversion to external type %s",
						inType.Name, inType.Name, outType.Name)
				} else if conversionHandled, err = g.Options.ExternalConversionsHandler(NewNamedVariable("&(*in)[i]", inType.Elem), NewNamedVariable("&(*out)[i]", outType.Elem), sw); err != nil {
					errors = append(errors, err)
				}

				if !conversionHandled {
					// so that the compiler doesn't barf
					sw.Do("_ = i\n", nil)
				}
			}
		}
		sw.Do("}\n", nil)
	}
	return
}

func (g *Generator) doStruct(inType, outType *types.Type, sw *generator.SnippetWriter) (errors []error) {
	for _, inMember := range inType.Members {
		if g.optedOut(inMember) {
			// This field is excluded from conversion.
			sw.Do("// INFO: in."+inMember.Name+" opted out of conversion generation\n", nil)
			continue
		}
		outMember, found := findMember(outType, inMember.Name)
		if !found {
			// This field doesn't exist in the peer.
			if g.Options.MissingFieldsHandler == nil {
				klog.Warningf("%s.%s requires manual conversion: does not exist in peer-type %s", inType.Name, inMember.Name, outType.Name)
			} else if err := g.Options.MissingFieldsHandler(NewNamedVariable("in", inType), NewNamedVariable("out", outType), &inMember, sw); err != nil {
				errors = append(errors, err)
			}
			continue
		}

		inMemberType, outMemberType := inMember.Type, outMember.Type
		// create a copy of both underlying types but give them the top level alias name (since aliases
		// are assignable)
		if underlying := unwrapAlias(inMemberType); underlying != inMemberType {
			copied := *underlying
			copied.Name = inMemberType.Name
			inMemberType = &copied
		}
		if underlying := unwrapAlias(outMemberType); underlying != outMemberType {
			copied := *underlying
			copied.Name = outMemberType.Name
			outMemberType = &copied
		}

		args := argsFromType(inMemberType, outMemberType).With("name", inMember.Name)

		// try a direct memory copy for any type that has exactly equivalent values
		if g.useUnsafeConversion(inMemberType, outMemberType) {
			args = args.With("Pointer", types.Ref("unsafe", "Pointer"))
			switch inMemberType.Kind {
			case types.Pointer:
				sw.Do("out.$.name$ = ($.outType|"+rawNamer+"$)($.Pointer|"+rawNamer+"$(in.$.name$))\n", args)
				continue
			case types.Map:
				sw.Do("out.$.name$ = *(*$.outType|"+rawNamer+"$)($.Pointer|"+rawNamer+"$(&in.$.name$))\n", args)
				continue
			case types.Slice:
				sw.Do("out.$.name$ = *(*$.outType|"+rawNamer+"$)($.Pointer|"+rawNamer+"$(&in.$.name$))\n", args)
				continue
			}
		}

		// check based on the top level name, not the underlying names
		if function, ok := g.preexists(inMember.Type, outMember.Type); ok {
			if g.functionHasTag(function, "drop") {
				continue
			}
			if !g.functionHasTag(function, "copy-only") || !isFastConversion(inMemberType, outMemberType) {
				args["function"] = function
				sw.Do("if err := $.function|"+rawNamer+"$(&in.$.name$, &out.$.name$"+g.extraArgumentsString()+"); err != nil {\n", args)
				sw.Do("return err\n", nil)
				sw.Do("}\n", nil)
				continue
			}
			klog.V(5).Infof("Skipped function %s because it is copy-only and we can use direct assignment", function.Name)
		}

		// If we can't auto-convert, punt before we emit any code.
		if inMemberType.Kind != outMemberType.Kind {
			if g.Options.InconvertibleFieldsHandler == nil {
				klog.Warningf("%s.%s requires manual conversion: inconvertible types: %s VS %s for %s.%s",
					inType.Name, inMember.Name, inMemberType, outMemberType, outType.Name, outMember.Name)
			} else if err := g.Options.InconvertibleFieldsHandler(NewNamedVariable("in", inType), NewNamedVariable("out", outType), &inMember, &outMember, sw); err != nil {
				errors = append(errors, err)
			}
			continue
		}

		switch inMemberType.Kind {
		case types.Builtin:
			if inMemberType == outMemberType {
				sw.Do("out.$.name$ = in.$.name$\n", args)
			} else {
				sw.Do("out.$.name$ = $.outType|"+rawNamer+"$(in.$.name$)\n", args)
			}
		case types.Map, types.Slice, types.Pointer:
			if isDirectlyAssignable(inMemberType, outMemberType) {
				sw.Do("out.$.name$ = in.$.name$\n", args)
				continue
			}

			sw.Do("if in.$.name$ != nil {\n", args)
			sw.Do("in, out := &in.$.name$, &out.$.name$\n", args)
			g.generateFor(inMemberType, outMemberType, sw)
			sw.Do("} else {\n", nil)
			sw.Do("out.$.name$ = nil\n", args)
			sw.Do("}\n", nil)
		case types.Struct:
			if isDirectlyAssignable(inMemberType, outMemberType) {
				sw.Do("out.$.name$ = in.$.name$\n", args)
				continue
			}
			if g.convertibleOnlyWithinPackage(inMemberType, outMemberType) {
				sw.Do("if err := "+conversionFunctionNameTemplate(publicImportTrackingNamer)+"(&in.$.name$, &out.$.name$"+g.extraArgumentsString()+"); err != nil {\n", args)
				sw.Do("return err\n}\n", nil)
			} else {
				errors = g.callExternalConversionsHandlerForStructField(inType, outType, inMemberType, outMemberType, &inMember, &outMember, sw, errors)
			}
		case types.Alias:
			if isDirectlyAssignable(inMemberType, outMemberType) {
				if inMemberType == outMemberType {
					sw.Do("out.$.name$ = in.$.name$\n", args)
				} else {
					sw.Do("out.$.name$ = $.outType|"+rawNamer+"$(in.$.name$)\n", args)
				}
			} else {
				if g.convertibleOnlyWithinPackage(inMemberType, outMemberType) {
					sw.Do("if err := "+conversionFunctionNameTemplate(publicImportTrackingNamer)+"(&in.$.name$, &out.$.name$"+g.extraArgumentsString()+"); err != nil {\n", args)
					sw.Do("return err\n}\n", nil)
				} else {
					errors = g.callExternalConversionsHandlerForStructField(inType, outType, inMemberType, outMemberType, &inMember, &outMember, sw, errors)
				}
			}
		default:
			if g.convertibleOnlyWithinPackage(inMemberType, outMemberType) {
				sw.Do("if err := "+conversionFunctionNameTemplate(publicImportTrackingNamer)+"(&in.$.name$, &out.$.name$"+g.extraArgumentsString()+"); err != nil {\n", args)
				sw.Do("return err\n}\n", nil)
			} else {
				errors = g.callExternalConversionsHandlerForStructField(inType, outType, inMemberType, outMemberType, &inMember, &outMember, sw, errors)
			}
		}
	}
	return
}

func (g *Generator) callExternalConversionsHandlerForStructField(inType, outType, inMemberType, outMemberType *types.Type, inMember, outMember *types.Member, sw *generator.SnippetWriter, errors []error) []error {
	if g.Options.ExternalConversionsHandler == nil {
		klog.Warningf("%s.%s requires manual conversion to external type %s.%s",
			inType.Name, inMember.Name, outType.Name, outMember.Name)
	} else {
		inVar := NewNamedVariable(fmt.Sprintf("&in.%s", inMember.Name), inMemberType)
		outVar := NewNamedVariable(fmt.Sprintf("&out.%s", outMember.Name), outMemberType)
		if _, err := g.Options.ExternalConversionsHandler(inVar, outVar, sw); err != nil {
			errors = append(errors, err)
		}
	}
	return errors
}

func (g *Generator) doPointer(inType, outType *types.Type, sw *generator.SnippetWriter) (errors []error) {
	sw.Do("*out = new($.Elem|"+rawNamer+"$)\n", outType)
	if isDirectlyAssignable(inType.Elem, outType.Elem) {
		if inType.Elem == outType.Elem {
			sw.Do("**out = **in\n", nil)
		} else {
			sw.Do("**out = $.|"+rawNamer+"$(**in)\n", outType.Elem)
		}
	} else {
		manualOrInternal := false

		if function, ok := g.preexists(inType.Elem, outType.Elem); ok {
			manualOrInternal = true
			sw.Do("if err := $.|"+rawNamer+"$(*in, *out"+g.extraArgumentsString()+"); err != nil {\n", function)
		} else if g.convertibleOnlyWithinPackage(inType.Elem, outType.Elem) {
			manualOrInternal = true
			sw.Do("if err := "+conversionFunctionNameTemplate(publicImportTrackingNamer)+"(*in, *out"+g.extraArgumentsString()+"); err != nil {\n", argsFromType(inType.Elem, outType.Elem))
		}

		if manualOrInternal {
			sw.Do("return err\n}\n", nil)
		} else if g.Options.ExternalConversionsHandler == nil {
			klog.Warningf("%s's values of type %s require manual conversion to external type %s",
				inType.Name, inType.Elem, outType.Name)
		} else if _, err := g.Options.ExternalConversionsHandler(NewNamedVariable("*in", inType), NewNamedVariable("*out", outType), sw); err != nil {
			errors = append(errors, err)
		}
	}
	return
}

func (g *Generator) doAlias(inType, outType *types.Type, sw *generator.SnippetWriter) []error {
	// TODO: Add support for aliases.
	return g.doUnknown(inType, outType, sw)
}

func (g *Generator) doUnknown(inType, outType *types.Type, sw *generator.SnippetWriter) []error {
	if g.Options.UnsupportedTypesHandler == nil {
		klog.Warningf("Don't know how to convert %s to %s", inType.Name, outType.Name)
	} else if err := g.Options.UnsupportedTypesHandler(NewNamedVariable("in", inType), NewNamedVariable("out", outType), sw); err != nil {
		return []error{err}
	}
	return nil
}

func (g *Generator) extraArgumentsString() string {
	result := ""
	for _, namedArgument := range g.Options.ManualConversionsTracker.additionalConversionArguments {
		result += ", " + namedArgument.Name
	}
	return result
}

// GetPeerTypeFor returns the peer type for type t.
func (g *Generator) GetPeerTypeFor(context *generator.Context, t *types.Type) *types.Type {
	if peerType, found := g.peerTypes[t.Name.Name]; found {
		return peerType
	}

	peerName := t.Name.Name
	if present, name := g.hasTagOption(t.CommentLines, "peerName"); present && len(name) != 0 {
		klog.V(5).Infof("Using custom peer name %q for input type %s", name, t.Name)
		peerName = name
	}

	var peerType *types.Type
	for _, peerPkgPath := range g.peerPackages {
		peerPkg := context.Universe[peerPkgPath]
		if peerPkg != nil && peerPkg.Has(peerName) {
			peerType = peerPkg.Types[peerName]
			break
		}
	}

	g.peerTypes[t.Name.Name] = peerType

	if peerType != nil {
		klog.V(5).Infof("Found peer type %s for input type %s", peerType, t)
	}

	return peerType
}

func (g *Generator) convertibleOnlyWithinPackage(inType, outType *types.Type) bool {
	var t, other *types.Type
	if inType.Name.Package == g.typesPackage.Path {
		t, other = inType, outType
	} else {
		t, other = outType, inType
	}

	if t.Name.Package != g.typesPackage.Path {
		return false
	}

	if g.optedOut(t) {
		klog.V(5).Infof("type %v requests no conversion generation, skipping", t)
		return false
	}

	return t.Kind == types.Struct && // TODO: Consider generating functions for other kinds too
		!namer.IsPrivateGoName(other.Name.Name) // filter out private types
}

// optedOut returns true iff type (or member) t has a comment tag of the form "<tag-name>=false"
// indicating that it's opting out of the conversion generation.
func (g *Generator) optedOut(t interface{}) bool {
	var commentLines []string
	switch in := t.(type) {
	case *types.Type:
		commentLines = in.CommentLines
	case types.Member:
		commentLines = in.CommentLines
	default:
		klog.Fatalf("don't know how to extract comment lines from %#v", t)
	}

	return g.hasTag(commentLines, "false")
}

func (g *Generator) noPublicFun(t *types.Type) bool {
	return g.hasTag(t.CommentLines, "no-public")
}

func (g *Generator) hasTag(comments []string, value string) bool {
	vals := g.extractTag(comments)
	for _, val := range vals {
		if val == value {
			return true
		}
	}
	return false
}

// extracts option tags, that is, tags of the form '+<tag-name>=<optionName>:<optionValue>'
func (g *Generator) hasTagOption(comments []string, optionName string) (bool, string) {
	vals := g.extractTag(comments)
	for _, val := range vals {
		split := strings.Split(val, ":")
		if len(split) == 2 && split[0] == optionName {
			return true, split[1]
		}
	}
	return false, ""
}

// TODO wkpo look at all comments, and document?
func (g *Generator) extractTag(comments []string) []string {
	return extractTag(g.Options.TagName, comments)
}

func (g *Generator) extractDocFileTag(tagName string) []string {
	return extractTag(tagName, g.typesPackage.Comments)
}

func extractTag(tagName string, comments []string) []string {
	if tagName == "" {
		return nil
	}
	return types.ExtractCommentTags("+", comments)[tagName]
}

func (g *Generator) functionHasTag(function *types.Type, tagValue string) bool {
	return functionHasTag(function, g.Options.FunctionTagName, tagValue)
}

func (g *Generator) preexists(inType, outType *types.Type) (*types.Type, bool) {
	return g.Options.ManualConversionsTracker.preexists(inType, outType)
}

func (g *Generator) useUnsafeConversion(t1, t2 *types.Type) bool {
	return !g.Options.NoUnsafeConversions && g.unsafeConversionArbitrator.canUseUnsafeConversion(t1, t2)
}

func (g *Generator) ManualConversions() map[ConversionPair]*types.Type {
	return g.Options.ManualConversionsTracker.conversionFunctions
}
