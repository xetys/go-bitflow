package steps

import (
	"errors"
	"fmt"

	"github.com/bitflow-stream/go-bitflow/bitflow"
	"github.com/bitflow-stream/go-bitflow/bitflow/fork"
	"github.com/bitflow-stream/go-bitflow/script/reg"
)

func RegisterOutputFiles(b reg.ProcessorRegistry) {
	create := func(p *bitflow.SamplePipeline, params map[string]string) error {
		filename := params["file"]
		if filename == "" {
			return reg.ParameterError("file", errors.New("Missing required parameter"))
		}
		delete(params, "file")

		var err error
		parallelize := reg.IntParam(params, "parallelize", 0, true, &err)
		if err != nil {
			return err
		}
		delete(params, "parallelize")

		distributor, err := _make_multi_file_pipeline_builder(params)
		if err == nil {
			distributor.Template = filename
			if parallelize > 0 {
				distributor.ExtendSubpipelines = func(fileName string, pipe *bitflow.SamplePipeline) {
					pipe.Add(&DecouplingProcessor{ChannelBuffer: parallelize})
				}
			}
			p.Add(&fork.SampleFork{Distributor: distributor})
		}
		return err
	}

	b.RegisterAnalysisParamsErr("output_files", create, "Output samples to multiple files, filenames are built from the given template, where placeholders like ${xxx} will be replaced with tag values")
}

func _make_multi_file_pipeline_builder(params map[string]string) (*fork.MultiFileDistributor, error) {
	if err := bitflow.DefaultEndpointFactory.ParseParameters(params); err != nil {
		return nil, fmt.Errorf("Error parsing parameters: %v", err)
	}
	output, err := bitflow.DefaultEndpointFactory.CreateOutput("file://-") // Create empty file output, will only be used as template with configuration values
	if err != nil {
		return nil, fmt.Errorf("Error creating template file output: %v", err)
	}
	fileOutput, ok := output.(*bitflow.FileSink)
	if !ok {
		return nil, fmt.Errorf("Error creating template file output, received wrong type: %T", output)
	}
	return &fork.MultiFileDistributor{Config: *fileOutput}, nil
}
