package steps

import (
	"errors"
	"fmt"

	bitflow "github.com/antongulenko/go-bitflow"
	pipeline "github.com/antongulenko/go-bitflow-pipeline"
	"github.com/antongulenko/go-bitflow-pipeline/fork"
	"github.com/antongulenko/go-bitflow-pipeline/query"
)

func RegisterOutputFiles(b *query.PipelineBuilder) {
	create := func(p *pipeline.SamplePipeline, params map[string]string) error {
		filename := params["file"]
		if filename == "" {
			return query.ParameterError("file", errors.New("Missing required parameter"))
		}
		delete(params, "file")

		distributor, err := _make_multi_file_pipeline_builder(params)
		distributor.Template = filename
		if err == nil {
			p.Add(&fork.SampleFork{Distributor: distributor})
		}
		return err
	}

	b.RegisterAnalysisParamsErr("output_files", create, "Output samples to multiple files, filenames are built from the given template, where placeholders like ${xxx} will be replaced with tag values", nil, "parallelize")
}

func _make_multi_file_pipeline_builder(params map[string]string) (*fork.MultiFileDistributor, error) {
	var endpointFactory bitflow.EndpointFactory
	if err := endpointFactory.ParseParameters(params); err != nil {
		return nil, fmt.Errorf("Error parsing parameters: %v", err)
	}
	output, err := endpointFactory.CreateOutput("file://-") // Create empty file output, will only be used as template with configuration values
	if err != nil {
		return nil, fmt.Errorf("Error creating template file output: %v", err)
	}
	fileOutput, ok := output.(*bitflow.FileSink)
	if !ok {
		return nil, fmt.Errorf("Error creating template file output, received wrong type: %T", output)
	}

	distributor := &fork.MultiFileDistributor{Config: *fileOutput}
	parallelize := query.IntParam(params, "parallelize", 0, true, &err)
	if err != nil {
		return nil, err
	}
	if parallelize > 0 {
		distributor.ExtendSubpipelines = func(fileName string, pipe *pipeline.SamplePipeline) {
			pipe.Add(&DecouplingProcessor{ChannelBuffer: parallelize})
		}
	}

	return distributor, nil
}
