package main

// TODO wkpo check all imports
import (
	"github.com/wk8/go-conversion-gen/pkg/converter"
	"k8s.io/klog/v2"
)

func main() {
	klog.InitFlags(nil)

	converter := converter.NewConverterFromCLIFlags()

	if err := converter.Run(); err != nil {
		klog.Fatalf("Error: %v", err)
	}
	klog.Infof("Completed successfully")
}

//func oldmain() { // TODO wkpo
//	klog.InitFlags(nil)
//
//	arguments := args.Default()
//
//	// TODO wkpo needed in the new flow/architecture???
//	// Override defaults.
//	arguments.OutputFileBaseName = "conversion_generated"
//	arguments.GoHeaderFilePath = ""
//	arguments.IncludeTestFiles = true // TODO wkpo needed??
//
//	// custom args
//	// TODO wkpo move to pkg?
//	customArgs := &pkg.CustomArgs{}
//	customArgs.AddFlags(pflag.CommandLine)
//	arguments.CustomArgs = customArgs
//
//	if err := arguments.Execute(
//		// TODO wkpo what are those again???
//		namer.NameSystems{
//			"wkpo": namer.NewRawNamer("", nil),
//		},
//		"wkpo", // default system
//		//generator.Packages,
//		func(context *generator.Context, generatorArgs *args.GeneratorArgs) generator.Packages {
//			return nil
//		},
//	); err != nil {
//		klog.Fatalf("Error: %v", err)
//	}
//	klog.Infof("Completed successfully")
//}
