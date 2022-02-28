package generator

import (
	"github.com/wk8/go-conversion-gen/pkg"
	"k8s.io/gengo/generator"
	"k8s.io/gengo/types"
)

// TODO wkpo look at all of these, check the comments are accurate and all tested?

type Options struct {
	// ManualConversionsTracker finds and caches which manually defined exist.
	// Trackers can be safely re-used across generators, for efficiency - otherwise it's perfectly
	// okay to leave this nil.
	ManualConversionsTracker *ManualConversionsTracker

	// if NoUnsafeConversions is set to true, it disables the use of unsafe conversions
	// between types that share the same memory layouts.
	NoUnsafeConversions bool

	// TagName is the marker that the generator will look for in types' comments:
	// "+<tag-name>=false" in a type's comment will instruct conversion-gen to skip that type.
	// TODO wkpo rename to TypeTagName ?
	TagName string

	// FunctionTagName is the marker that the generator will look for in functions' comments, in
	// particular for manual conversion functions:
	// "+<tag-name>=drop" in a manual conversion function's comment means to drop that conversion altogether.
	// TODO wkpo grep for "copy-only" and remove!
	FunctionTagName string

	// MissingFieldsHandler allows setting a callback to decide what happens when converting
	// from inVar.Type to outVar.Type, and when inVar.Type's member doesn't exist in outType.
	// The callback can freely write into the snippet writer, at the spot in the auto-generated
	// conversion function where the conversion code for that field should be.
	// If the handler returns an error, the auto-generated private conversion function
	// (i.e. autoConvert_a_X_To_b_Y) will still be generated, but not the public wrapper for it
	// (i.e. Convert_a_X_To_b_Y).
	// The handler can also choose to panic to stop the generation altogether, e.g. by calling
	// klog.Fatalf.
	// If this is not set, missing fields are silently ignored.
	// Note that the snippet writer's context is that of the generator (in particular, it can use
	// any namers defined by the generator).
	MissingFieldsHandler func(inVar, outVar NamedVariable, member *types.Member, sw *generator.SnippetWriter) error

	// InconvertibleFieldsHandler allows setting a callback to decide what happens when converting
	// from inVar.Type to outVar.Type, and when inVar.Type's inMember and outVar.Type's outMember are of
	// inconvertible types.
	// Same as for other handlers, the callback can freely write into the snippet writer, at the spot in
	// the auto-generated conversion function where the conversion code for that field should be.
	// If the handler returns an error, the auto-generated private conversion function
	// (i.e. autoConvert_a_X_To_b_Y) will still be generated, but not the public wrapper for it
	// (i.e. Convert_a_X_To_b_Y).
	// The handler can also choose to panic to stop the generation altogether, e.g. by calling
	// klog.Fatalf.
	// If this is not set, missing fields are silently ignored.
	// Note that the snippet writer's context is that of the generator (in particular, it can use
	// any namers defined by the generator).
	InconvertibleFieldsHandler func(inVar, outVar NamedVariable, inMember, outMember *types.Member, sw *generator.SnippetWriter) error

	// UnsupportedTypesHandler allows setting a callback to decide what happens when converting
	// from inVar.Type to outVar.Type, and this generator has no idea how to handle that conversion.
	// Same as for other handlers, the callback can freely write into the snippet writer, at the spot in
	// the auto-generated conversion function where the conversion code for that type should be.
	// If the handler returns an error, the auto-generated private conversion function
	// (i.e. autoConvert_a_X_To_b_Y) will still be generated, but not the public wrapper for it
	// (i.e. Convert_a_X_To_b_Y).
	// The handler can also choose to panic to stop the generation altogether, e.g. by calling
	// klog.Fatalf.
	// If this is not set, missing fields are silently ignored.
	// Note that the snippet writer's context is that of the generator (in particular, it can use
	// any namers defined by the generator).
	UnsupportedTypesHandler func(inVar, outVar NamedVariable, sw *generator.SnippetWriter) error

	// ExternalConversionsHandler allows setting a callback to decide what happens when converting
	// from inVar.Type to outVar.Type, but outVar.Type is in a different package than inVar.Type - and so
	// this generator can't know where to find a conversion function for that.
	// Same as for other handlers, the callback can freely write into the snippet writer, at the spot in
	// the auto-generated conversion function where the conversion code for that type should be.
	// If the handler returns an error, the auto-generated private conversion function
	// (i.e. autoConvert_a_X_To_b_Y) will still be generated, but not the public wrapper for it
	// (i.e. Convert_a_X_To_b_Y).
	// The handler can also choose to panic to stop the generation altogether, e.g. by calling
	// klog.Fatalf.
	// If this is not set, missing fields are silently ignored.
	// The boolean returned by the handler should indicate whether it has written code to handle
	// the conversion.
	// Note that the snippet writer's context is that of the generator (in particular, it can use
	// any namers defined by the generator).
	ExternalConversionsHandler func(inVar, outVar NamedVariable, sw *generator.SnippetWriter) (bool, error)
}

func DefaultOptions() *Options {
	return &Options{
		TagName:         pkg.DefaultTagName,
		FunctionTagName: pkg.DefaultTagName,
	}
}
