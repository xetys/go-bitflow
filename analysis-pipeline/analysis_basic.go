package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	log "github.com/Sirupsen/logrus"

	. "github.com/antongulenko/data2go/analysis"
	"github.com/antongulenko/data2go/sample"
	"github.com/antongulenko/golib"
)

var (
	metric_filter_include golib.StringSlice
	metric_filter_exclude golib.StringSlice
)

func init() {
	RegisterSampleHandler("src", &SampleTagger{SourceTags: []string{SourceTag}})
	RegisterSampleHandler("src-append", &SampleTagger{SourceTags: []string{SourceTag}, DontOverwrite: true})

	RegisterAnalysisParams("decouple", decouple_samples, "number of buffered samples")
	RegisterAnalysis("merge_headers", merge_headers)
	RegisterAnalysisParams("pick", pick_x_percent, "samples to keep 0..1")
	RegisterAnalysisParams("head", pick_head, "number of first samples to keep")
	RegisterAnalysis("print", print_samples)
	RegisterAnalysisParams("filter_tag", filter_tag, "tag=value or tag!=value")

	RegisterAnalysis("shuffle", shuffle_data)
	RegisterAnalysisParams("sort", sort_data, "comma-separated list of tags")

	RegisterAnalysis("scale_min_max", normalize_min_max)
	RegisterAnalysis("standardize", normalize_standardize)

	RegisterAnalysisParams("plot", plot, "[<color tag>,]<output filename>")
	RegisterAnalysisParams("plot_separate", separate_plots, "same as plot")
	RegisterAnalysisParams("stats", feature_stats, "output filename for metric statistics")

	RegisterAnalysisParams("remap", remap_features, "comma-separated list of metrics")
	RegisterAnalysisParams("filter_variance", filter_variance, "minimum weighted stddev of the population (stddev / mean)")

	RegisterAnalysis("filter_metrics", filter_metrics)
	flag.Var(&metric_filter_include, "metrics_include", "Include regex used with '-e filter_metrics'")
	flag.Var(&metric_filter_exclude, "metrics_exclude", "Exclude regex used with '-e filter_metrics'")
}

func print_samples(p *SamplePipeline) {
	p.Add(new(SamplePrinter))
}

func shuffle_data(p *SamplePipeline) {
	p.Batch(new(SampleShuffler))
}

func sort_data(p *SamplePipeline, params string) {
	var tags []string
	if params != "" {
		tags = strings.Split(params, ",")
	}
	p.Batch(&SampleSorter{tags})
}

func merge_headers(p *SamplePipeline) {
	p.Add(NewMultiHeaderMerger())
}

func normalize_min_max(p *SamplePipeline) {
	p.Batch(new(MinMaxScaling))
}

func normalize_standardize(p *SamplePipeline) {
	p.Batch(new(StandardizationScaling))
}

func pick_x_percent(p *SamplePipeline, params string) {
	pick_percentage, err := strconv.ParseFloat(params, 64)
	if err != nil {
		log.Fatalln("Failed to parse parameter for -e pick:", err)
	}
	counter := float64(0)
	p.Add(&SampleFilter{
		Description: fmt.Sprintf("Pick %.2f%%", pick_percentage*100),
		IncludeFilter: func(inSample *sample.Sample) bool {
			counter += pick_percentage
			if counter > 1.0 {
				counter -= 1.0
				return true
			}
			return false
		},
	})
}

func filter_metrics(p *SamplePipeline) {
	filter := NewMetricFilter()
	for _, include := range metric_filter_include {
		filter.IncludeRegex(include)
	}
	for _, exclude := range metric_filter_exclude {
		filter.ExcludeRegex(exclude)
	}
	p.Add(filter)
}

func filter_tag(p *SamplePipeline, params string) {
	val := ""
	equals := true
	index := strings.Index(params, "!=")
	if index >= 0 {
		val = params[index+2:]
		equals = false
	} else {
		index = strings.IndexRune(params, '=')
		if index == -1 {
			log.Fatalln("Parameter for -e filter_tag must be '<tag>=<value>' or '<tag>!=<value>'")
		} else {
			val = params[index+1:]
		}
	}
	tag := params[:index]
	sign := "!="
	if equals {
		sign = "=="
	}
	p.Add(&SampleFilter{
		Description: fmt.Sprintf("Filter tag %v %s %v", tag, sign, val),
		IncludeFilter: func(inSample *sample.Sample) bool {
			if equals {
				return inSample.Tag(tag) == val
			} else {
				return inSample.Tag(tag) != val
			}
		},
	})
}

func plot(pipe *SamplePipeline, params string) {
	do_plot(pipe, params, false)
}

func separate_plots(pipe *SamplePipeline, params string) {
	do_plot(pipe, params, true)
}

func do_plot(pipe *SamplePipeline, params string, separatePlots bool) {
	if params == "" {
		log.Fatalln("-e plot needs parameters (-e plot,[<tag>,]<filename>)")
	}
	index := strings.IndexRune(params, ',')
	tag := ""
	filename := params
	if index == -1 {
		log.Warnln("-e plot got no tag parameter, not coloring plot (-e plot,[<tag>,]<filename>)")
	} else {
		tag = params[:index]
		filename = params[index+1:]
	}
	pipe.Add(&Plotter{OutputFile: filename, ColorTag: tag, SeparatePlots: separatePlots})
}

func decouple_samples(pipe *SamplePipeline, params string) {
	buf := 150000
	if params != "" {
		var err error
		if buf, err = strconv.Atoi(params); err != nil {
			log.Fatalln("Failed to parse parameter for -e decouple:", err)
		}
	} else {
		log.Warnln("No parameter for -e decouple, default channel buffer:", buf)
	}
	pipe.Add(&DecouplingProcessor{ChannelBuffer: buf})
}

func feature_stats(pipe *SamplePipeline, params string) {
	if params == "" {
		log.Fatalln("-e stats needs parameter: file to store feature statistics")
	} else {
		pipe.Add(NewStoreStats(params))
	}
}

func remap_features(pipe *SamplePipeline, params string) {
	var metrics []string
	if params != "" {
		metrics = strings.Split(params, ",")
	}
	pipe.Add(NewMetricMapper(metrics))
}

func filter_variance(pipe *SamplePipeline, params string) {
	variance, err := strconv.ParseFloat(params, 64)
	if err != nil {
		log.Fatalln("Error parsing parameter for -e filter_variance:", err)
	}
	pipe.Batch(NewMetricVarianceFilter(variance))
}

func pick_head(pipe *SamplePipeline, params string) {
	num, err := strconv.Atoi(params)
	if err != nil {
		log.Fatalln("Error parsing parameter for -e head:", err)
	}
	pipe.Add(&PickHead{Num: num})
}

type PickHead struct {
	AbstractProcessor
	Num       int // parameter
	processed int // internal variable
}

func (head *PickHead) Sample(sample *sample.Sample, header *sample.Header) error {
	if err := head.Check(sample, header); err != nil {
		return err
	}
	if head.Num > head.processed {
		head.processed++
		return head.OutgoingSink.Sample(sample, header)
	} else {
		return nil
	}
}

func (head *PickHead) String() string {
	return "Pick first " + strconv.Itoa(head.Num) + " samples"
}

type SampleTagger struct {
	SourceTags    []string
	DontOverwrite bool
}

func (h *SampleTagger) HandleHeader(header *sample.Header, source string) {
	header.HasTags = true
}

func (h *SampleTagger) HandleSample(sample *sample.Sample, source string) {
	for _, tag := range h.SourceTags {
		if h.DontOverwrite {
			base := tag
			tag = base
			for i := 0; sample.HasTag(tag); i++ {
				tag = base + strconv.Itoa(i)
			}
		}
		sample.SetTag(tag, source)
	}
}