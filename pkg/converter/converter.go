package converter

// TODO wkpo lint and goimports...
import (
	"fmt"
	"github.com/spf13/pflag"
	"github.com/wk8/go-conversion-gen/pkg/generator"
	"k8s.io/gengo/args"
	gengogenerator "k8s.io/gengo/generator"
	"k8s.io/gengo/namer"
	"k8s.io/gengo/types"
	"k8s.io/klog/v2"
	"path/filepath"
)

type Converter struct {
	Options *Options

	args *args.GeneratorArgs
}

func NewConverter(targetPackages []string, options *Options) *Converter {
	args := defaultGenericArgs()
	args.WithoutDefaultFlagParsing()

	if options == nil {
		options = DefaultOptions()
	}
	if options.GeneratorOptions == nil {
		options.GeneratorOptions = generator.DefaultOptions()
	}

	args.OutputFileBaseName = options.OutputFileBaseName
	args.InputDirs = targetPackages

	return &Converter{
		Options: options,
		args:    args,
	}
}

type customCLIArgs struct {
	noUnsafeConversions               bool
	tagName                           string
	functionTagName                   string
	peerPackagesTagName               string
	basePeerPackages                  []string
	noPublicConversionFunctionOnError bool
}

// TODO wkpo makes sense? should it be called on
// addFlags add the generator flags to the flag set.
func (ca *customCLIArgs) addFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&ca.noUnsafeConversions, "skip-unsafe", ca.noUnsafeConversions,
		"If true, will not generate code using unsafe pointer conversions; resulting code may be slower.")
	fs.StringVar(&ca.tagName, "tag-name", ca.tagName,
		"comment tag. \"+<tag-name>=false\" in a type's comment will skip that type.")
	fs.StringVar(&ca.functionTagName, "function-tag-name", ca.functionTagName,
		"\"+<tag-name>=drop\" in a manual conversion function's comment means to drop that conversion altogether.")
	// TODO wkpo i think the syntax is wrong down below, not comma separated
	fs.StringVar(&ca.peerPackagesTagName, "peer-packages-tag-name", ca.peerPackagesTagName,
		"\"+<tag-name>=<peer-pkg-1>,<peer-pkg-2>\" in an input package's doc.go file will instruct the converter to look for that package's peer types in the specified peer packages")
	fs.StringSliceVar(&ca.basePeerPackages, "base-peer-packages", ca.basePeerPackages,
		"Comma-separated list of peer packages to be shared between all inputs - that's where the converter looks for peer types to generate conversion functions.")
	fs.BoolVar(&ca.noPublicConversionFunctionOnError, "no-public-conversion-function-on-error", ca.noPublicConversionFunctionOnError,
		"If true, will not generate a public conversion function if it's unable to generate conversion code for any field - it will still generate a private conversion function that you can then wrap in your own public function.")
}

func (ca *customCLIArgs) populateOptions(options *Options) {
	if ca.noUnsafeConversions {
		options.GeneratorOptions.NoUnsafeConversions = true
	}
	if ca.tagName != "" {
		options.GeneratorOptions.TagName = ca.tagName
	}
	if ca.functionTagName != "" {
		options.GeneratorOptions.FunctionTagName = ca.functionTagName
	}
	if ca.peerPackagesTagName != "" {
		options.GeneratorOptions.PeerPackagesTagName = ca.peerPackagesTagName
	}
	if len(ca.basePeerPackages) != 0 {
		options.BasePeerPackages = ca.basePeerPackages
	}
	if ca.noPublicConversionFunctionOnError {
		options.GeneratorOptions.MissingFieldsHandler = ErrorMissingFieldHandler
		options.GeneratorOptions.InconvertibleFieldsHandler = ErrorInconvertibleFieldsHandler

		// TODO wkpo UnsupportedTypesHandler and ExternalConversionsHandler?
	}
}

// ErrorMissingFieldHandler is a missing field handler that will prevent the generation of public conversion functions for structs that have one or more field
// that are missing conversion functions.
func ErrorMissingFieldHandler(inVar, outVar generator.NamedVariable, member *types.Member, sw *gengogenerator.SnippetWriter) error {
	sw.Do("// WARNING: in."+member.Name+" requires manual conversion: does not exist in peer-type\n", nil)
	return fmt.Errorf("field " + member.Name + " requires manual conversion")
}

// ErrorInconvertibleFieldsHandler is a missing field handler that will prevent the generation of public conversion functions for structs that have one or more field
// that are inconvertible.
func ErrorInconvertibleFieldsHandler(inVar, outVar generator.NamedVariable, inMember, outMember *types.Member, sw *gengogenerator.SnippetWriter) error {
	sw.Do("// WARNING: in."+inMember.Name+" requires manual conversion: inconvertible types ("+
		inMember.Type.String()+" vs "+outMember.Type.String()+")\n", nil)
	return fmt.Errorf("field " + inMember.Name + " requires manual conversion")
}

func NewConverterFromCLIFlags() *Converter {
	args := defaultGenericArgs()

	customArgs := &customCLIArgs{}
	customArgs.addFlags(pflag.CommandLine)
	args.CustomArgs = customArgs

	return &Converter{
		Options: DefaultOptions(),
		args:    args,
	}
}

// Run runs the converter
func (c *Converter) Run() error {
	return c.args.Execute(
		namer.NameSystems{
			"conversion": generator.ConversionNamer(),
		},
		"conversion",
		c.packages,
	)
}

func (c *Converter) packages(context *gengogenerator.Context, arguments *args.GeneratorArgs) (packages gengogenerator.Packages) {
	var boilerplate []byte

	customArgs, fromCLI := arguments.CustomArgs.(*customCLIArgs)
	if fromCLI {
		customArgs.populateOptions(c.Options)

		if arguments.GoHeaderFilePath != "" {
			var err error
			boilerplate, err = arguments.LoadGoBoilerplate()
			if err != nil {
				klog.Fatalf("Failed loading boilerplate: %v", err)
			}
		}
	}

	header := append([]byte(fmt.Sprintf("// +build !%s\n\n", arguments.GeneratedBuildTag)), boilerplate...)

	// share a manual conversion tracker between packages for efficiency
	if c.Options.GeneratorOptions.ManualConversionsTracker == nil {
		c.Options.GeneratorOptions.ManualConversionsTracker = generator.NewManualConversionsTracker()
	}

	processed := map[string]bool{}
	for _, i := range context.Inputs {
		// skip duplicates
		if processed[i] {
			continue
		}
		processed[i] = true

		klog.V(5).Infof("considering pkg %q", i)
		pkg := context.Universe[i]
		if pkg == nil {
			// if the input had no Go files, for example.
			continue
		}

		// TODO wkpo all that stuff about external types...?

		conversionGenerator, err := generator.NewConversionGenerator(
			context,
			arguments.OutputFileBaseName,
			pkg.Path,
			pkg.Path, // TODO wkpo why the 2 args???
			c.Options.BasePeerPackages,
			c.Options.GeneratorOptions,
		)
		if err != nil {
			klog.Fatalf("unable to build conversion generator for %v: %v", pkg, err)
		}

		packages = append(packages,
			&gengogenerator.DefaultPackage{
				PackageName: filepath.Base(pkg.Path),
				PackagePath: pkg.Path,
				HeaderText:  header,
				GeneratorFunc: func(context *gengogenerator.Context) []gengogenerator.Generator {
					generators := []gengogenerator.Generator{conversionGenerator}

					if c.Options.ExtraGenerators != nil {
						extraGenerators, err := c.Options.ExtraGenerators(context, conversionGenerator)
						if err != nil {
							klog.Fatalf("unable to build extra generators for %v: %v", pkg, err)
						}
						generators = append(generators, extraGenerators...)
					}

					return generators
				},
				FilterFunc: func(c *gengogenerator.Context, t *types.Type) bool {
					return t.Name.Package == pkg.Path
				},
			})
	}

	return
}

func defaultGenericArgs() *args.GeneratorArgs {
	args := args.Default()
	args.GoHeaderFilePath = ""
	return args
}
