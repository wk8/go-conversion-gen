package converter

import (
	"github.com/wk8/go-conversion-gen/pkg"
	"github.com/wk8/go-conversion-gen/pkg/generator"
)

// TODO wkpo look at all of these, check the comments are accurate and all tested?

type Options struct {
	// GeneratorOptions will be passed down to the Generators this converter spawns.
	GeneratorOptions *generator.Options

	// PeerPackagesTagName is the marker that the converter will look for in the doc.go file
	// of input packages.
	// TODO wkpo check that this syntax is the right one? for several pkgs?
	// "+<tag-name>=<peer-pkg-1>,<peer-pkg-2>" in an input package's doc.go file will instruct
	// the converter to look for that package's peer types in the specified peer packages.
	PeerPackagesTagName string

	// BasePeerPackages are the peer packages to be shared between all inputs.
	BasePeerPackages []string

	// TODO wkpo externalTypesTagName??
}

func DefaultOptions() *Options {
	return &Options{
		GeneratorOptions:    generator.DefaultOptions(),
		PeerPackagesTagName: pkg.DefaultTagName,
	}
}
