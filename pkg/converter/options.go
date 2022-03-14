package converter

import (
	gengogenerator "k8s.io/gengo/generator"

	"github.com/wk8/go-conversion-gen/pkg/generator"
)

// TODO wkpo look at all of these, check the comments are accurate and all tested?

type Options struct {
	// GeneratorOptions will be passed down to the Generators this converter spawns.
	GeneratorOptions *generator.Options

	// OutputFileBaseName is the name of the generated file in each target/input package.
	OutputFileBaseName string

	// BasePeerPackages are the peer packages to be shared between all inputs.
	BasePeerPackages []string

	// TODO wkpo externalTypesTagName??

	// ExtraGenerators allows adding more gengo generators, if needed.
	ExtraGenerators func(context *gengogenerator.Context, conversionGenerator *generator.Generator) ([]gengogenerator.Generator, error)
}

func DefaultOptions() *Options {
	return &Options{
		GeneratorOptions: generator.DefaultOptions(),

		OutputFileBaseName: "conversion_generated",
	}
}
