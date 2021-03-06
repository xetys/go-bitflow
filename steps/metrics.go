package steps

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antongulenko/go-onlinestats"
	"github.com/bitflow-stream/go-bitflow/bitflow"
	"github.com/bitflow-stream/go-bitflow/script/reg"
	log "github.com/sirupsen/logrus"
)

type MetricMapperHelper struct {
	bitflow.HeaderChecker
	outHeader  *bitflow.Header
	outIndices []int
}

func (helper *MetricMapperHelper) incomingHeader(header *bitflow.Header, description fmt.Stringer, constructIndices func(header *bitflow.Header) ([]int, []string)) error {
	if !helper.HeaderChanged(header) {
		return nil
	}
	var outFields []string
	helper.outIndices, outFields = constructIndices(header)
	if len(helper.outIndices) != len(outFields) {
		return errors.New("constructIndices() in MetricMapperHelper.incomingHeader returned non equal sized results")
	}
	if len(outFields) == 0 {
		log.Warnln(description, "removed all metrics")
	} else {
		log.Println(description, "changes metrics", len(header.Fields), "->", len(outFields))
	}
	helper.outHeader = header.Clone(outFields)
	return nil
}

func (helper *MetricMapperHelper) convertValues(sample *bitflow.Sample) {
	// Temporary copy to avoid overwriting values. Can be allocated on the stack.
	inValues := make([]bitflow.Value, len(sample.Values))
	copy(inValues, sample.Values)

	sample.Resize(len(helper.outIndices))
	for i, index := range helper.outIndices {
		sample.Values[i] = inValues[index]
	}
}

func (helper *MetricMapperHelper) convertSample(sample *bitflow.Sample) *bitflow.Sample {
	outSample := sample.Clone()
	helper.convertValues(outSample)
	return outSample
}

type AbstractMetricMapper struct {
	bitflow.NoopProcessor
	Description      fmt.Stringer
	ConstructIndices func(header *bitflow.Header) ([]int, []string)

	helper MetricMapperHelper
}

func (m *AbstractMetricMapper) Sample(sample *bitflow.Sample, header *bitflow.Header) error {
	if err := m.helper.incomingHeader(header, m, m.ConstructIndices); err != nil {
		return err
	}
	sample = m.helper.convertSample(sample)
	return m.NoopProcessor.Sample(sample, m.helper.outHeader)
}

func (m *AbstractMetricMapper) String() string {
	if desc := m.Description; desc == nil {
		return "Abstract Metric Mapper"
	} else {
		return desc.String()
	}
}

type AbstractMetricFilter struct {
	AbstractMetricMapper
	IncludeFilter func(name string) bool // Return true if metric should be included
}

func (filter *AbstractMetricFilter) constructIndices(header *bitflow.Header) ([]int, []string) {
	outFields := make([]string, 0, len(header.Fields))
	outIndices := make([]int, 0, len(header.Fields))
	include := filter.IncludeFilter
	if include == nil {
		return nil, nil
	}
	for index, field := range header.Fields {
		if include(field) {
			outFields = append(outFields, field)
			outIndices = append(outIndices, index)
		}
	}
	return outIndices, outFields
}

type MetricFilter struct {
	AbstractMetricFilter
	exclude []*regexp.Regexp
	include []*regexp.Regexp
}

func NewMetricFilter() *MetricFilter {
	res := new(MetricFilter)
	res.Description = res
	res.ConstructIndices = res.constructIndices
	res.IncludeFilter = res.filter
	return res
}

func RegisterIncludeMetricsFilter(b reg.ProcessorRegistry) {
	b.RegisterAnalysisParamsErr("include",
		func(p *bitflow.SamplePipeline, params map[string]string) error {
			filter, err := NewMetricFilter().IncludeRegex(params["m"])
			if err == nil {
				p.Add(filter)
			}
			return err
		},
		"Match every metric with the given regex and only include the matched metrics", reg.RequiredParams("m"))
}

func RegisterExcludeMetricsFilter(b reg.ProcessorRegistry) {
	b.RegisterAnalysisParamsErr("exclude",
		func(p *bitflow.SamplePipeline, params map[string]string) error {
			filter, err := NewMetricFilter().ExcludeRegex(params["m"])
			if err == nil {
				p.Add(filter)
			}
			return err
		},
		"Match every metric with the given regex and exclude the matched metrics", reg.RequiredParams("m"))
}

func (filter *MetricFilter) Exclude(regex *regexp.Regexp) *MetricFilter {
	filter.exclude = append(filter.exclude, regex)
	return filter
}

func (filter *MetricFilter) ExcludeStr(substr string) *MetricFilter {
	res, err := filter.ExcludeRegex(regexp.QuoteMeta(substr))
	if err != nil {
		panic(err)
	}
	return res
}

func (filter *MetricFilter) ExcludeRegex(regexStr string) (*MetricFilter, error) {
	regex, err := regexp.Compile(regexStr)
	if err != nil {
		return nil, err
	}
	return filter.Exclude(regex), nil
}

func (filter *MetricFilter) Include(regex *regexp.Regexp) *MetricFilter {
	filter.include = append(filter.include, regex)
	return filter
}

func (filter *MetricFilter) IncludeStr(substr string) *MetricFilter {
	res, err := filter.IncludeRegex(regexp.QuoteMeta(substr))
	if err != nil {
		panic(err)
	}
	return res
}

func (filter *MetricFilter) IncludeRegex(regexStr string) (*MetricFilter, error) {
	regex, err := regexp.Compile(regexStr)
	if err != nil {
		return nil, err
	}
	return filter.Include(regex), nil
}

func (filter *MetricFilter) filter(name string) bool {
	excluded := false
	for _, regex := range filter.exclude {
		if excluded = regex.MatchString(name); excluded {
			break
		}
	}
	if !excluded && len(filter.include) > 0 {
		excluded = true
		for _, regex := range filter.include {
			if excluded = !regex.MatchString(name); !excluded {
				break
			}
		}
	}
	return !excluded
}

func (filter *MetricFilter) MergeProcessor(other bitflow.SampleProcessor) bool {
	if otherFilter, ok := other.(*MetricFilter); !ok {
		return false
	} else {
		filter.exclude = append(filter.exclude, otherFilter.exclude...)
		filter.include = append(filter.include, otherFilter.include...)
		return true
	}
}

func (filter *MetricFilter) String() string {
	return fmt.Sprintf("MetricFilter(%v exclude filters, %v include filters)", len(filter.exclude), len(filter.include))
}

type MetricMapper struct {
	AbstractMetricMapper
	Metrics []string
}

func NewMetricMapper(metrics []string) *MetricMapper {
	mapper := &MetricMapper{
		Metrics: metrics,
	}
	mapper.Description = mapper
	mapper.ConstructIndices = mapper.constructIndices
	return mapper
}

func RegisterMetricMapper(b reg.ProcessorRegistry) {
	b.RegisterAnalysisParams("remap",
		func(p *bitflow.SamplePipeline, params map[string]string) {
			metrics := strings.Split(params["header"], ",")
			p.Add(NewMetricMapper(metrics))
		},
		"Change (reorder) the header to the given comma-separated list of metrics", reg.RequiredParams("header"))
}

func (mapper *MetricMapper) constructIndices(header *bitflow.Header) ([]int, []string) {
	fields := make([]int, 0, len(mapper.Metrics))
	metrics := make([]string, 0, len(mapper.Metrics))
	for _, metric := range mapper.Metrics {
		found := false
		for field, inMetric := range header.Fields {
			if metric == inMetric {
				fields = append(fields, field)
				metrics = append(metrics, metric)
				found = true
				break
			}
		}
		if !found {
			log.Warnf("%v: metric %v not found", mapper, metric)
		}
	}
	return fields, metrics
}

func (mapper *MetricMapper) String() string {
	maxLen := 3
	if len(mapper.Metrics) > maxLen {
		return fmt.Sprintf("Metric Mapper: %v ...", mapper.Metrics[:maxLen])
	} else {
		return fmt.Sprintf("Metric Mapper: %v", mapper.Metrics)
	}
}

type AbstractBatchMetricMapper struct {
	Description      fmt.Stringer
	ConstructIndices func(header *bitflow.Header, samples []*bitflow.Sample) ([]int, []string)
}

func (mapper *AbstractBatchMetricMapper) ProcessBatch(header *bitflow.Header, samples []*bitflow.Sample) (*bitflow.Header, []*bitflow.Sample, error) {
	var helper MetricMapperHelper
	constructIndices := func(_ *bitflow.Header) ([]int, []string) {
		return mapper.ConstructIndices(header, samples)
	}
	if err := helper.incomingHeader(header, mapper, constructIndices); err != nil {
		return nil, nil, err
	}
	for _, sample := range samples {
		helper.convertValues(sample)
	}
	return helper.outHeader, samples, nil
}

func (mapper *AbstractBatchMetricMapper) String() string {
	if desc := mapper.Description; desc == nil {
		return "Abstract Batch Metric Mapper"
	} else {
		return desc.String()
	}
}

func NewMetricVarianceFilter(minimumWeightedStddev float64) *AbstractBatchMetricMapper {
	return &AbstractBatchMetricMapper{
		Description: bitflow.String(fmt.Sprintf("Metric Variance Filter (%.2f%%)", minimumWeightedStddev*100)),
		ConstructIndices: func(header *bitflow.Header, samples []*bitflow.Sample) ([]int, []string) {
			numFields := len(header.Fields)
			variances := make([]onlinestats.Running, numFields)
			for _, sample := range samples {
				for i := range header.Fields {
					variances[i].Push(float64(sample.Values[i]))
				}
			}
			indices := make([]int, 0, numFields)
			fields := make([]string, 0, numFields)
			for i, field := range header.Fields {
				weighted_stddev := variances[i].Stddev()
				if mean := variances[i].Mean(); mean != 0 {
					weighted_stddev /= mean
				}
				if weighted_stddev >= minimumWeightedStddev {
					indices = append(indices, i)
					fields = append(fields, field)
				}
			}
			return indices, fields
		},
	}
}

func RegisterVarianceMetricsFilter(b reg.ProcessorRegistry) {
	b.RegisterAnalysisParamsErr("filter_variance",
		func(p *bitflow.SamplePipeline, params map[string]string) error {
			variance, err := strconv.ParseFloat(params["min"], 64)
			if err != nil {
				err = reg.ParameterError("min", err)
			} else {
				p.Batch(NewMetricVarianceFilter(variance))
			}
			return err
		},
		"In a batch of samples, filter out the metrics with a variance lower than the given threshold (based on the weighted stddev of the population, stddev/mean)",
		reg.RequiredParams("min"), reg.SupportBatch())
}

type MetricRenamer struct {
	AbstractMetricMapper
	regexes      []*regexp.Regexp
	replacements []string
}

func NewMetricRenamer(regexes []*regexp.Regexp, replacements []string) *MetricRenamer {
	if len(regexes) != len(replacements) {
		panic(fmt.Sprintf("MetricRenamer: number of regexes does not match number of replacements (%v != %v)", len(regexes), len(replacements)))
	}
	renamer := &MetricRenamer{
		regexes:      regexes,
		replacements: replacements,
	}
	renamer.Description = renamer
	renamer.ConstructIndices = renamer.constructIndices
	return renamer
}

func RegisterMetricRenamer(b reg.ProcessorRegistry) {
	b.RegisterAnalysisParamsErr("rename",
		func(p *bitflow.SamplePipeline, params map[string]string) error {
			if len(params) == 0 {
				return errors.New("Need at least one regex=replacement parameter")
			}

			var regexes []*regexp.Regexp
			var replacements []string
			for regex, replacement := range params {
				r, err := regexp.Compile(regex)
				if err != nil {
					return reg.ParameterError(regex, err)
				}
				regexes = append(regexes, r)
				replacements = append(replacements, replacement)
			}
			p.Add(NewMetricRenamer(regexes, replacements))
			return nil
		},
		"Find the keys (regexes) in every metric name and replace the matched parts with the given values")
}

func (r *MetricRenamer) String() string {
	return fmt.Sprintf("Metric renamer (%v regexes)", len(r.regexes))
}

func (r *MetricRenamer) constructIndices(header *bitflow.Header) ([]int, []string) {
	fields := make(indexedFields, len(header.Fields))
	for i, field := range header.Fields {
		for i, regex := range r.regexes {
			replace := r.replacements[i]
			field = regex.ReplaceAllString(field, replace)
		}
		fields[i].index = i
		fields[i].field = field
	}
	sort.Sort(fields)
	indices := make([]int, len(fields))
	outFields := make([]string, len(fields))
	for i, field := range fields {
		indices[i] = field.index
		outFields[i] = field.field
	}
	return indices, outFields
}

func (r *MetricRenamer) MergeProcessor(other bitflow.SampleProcessor) bool {
	if otherFilter, ok := other.(*MetricRenamer); !ok {
		return false
	} else {
		r.regexes = append(r.regexes, otherFilter.regexes...)
		r.replacements = append(r.replacements, otherFilter.replacements...)
		return true
	}
}

type indexedFields []struct {
	index int
	field string
}

func (f indexedFields) Len() int {
	return len(f)
}

func (f indexedFields) Less(i, j int) bool {
	return f[i].field < f[j].field
}

func (f indexedFields) Swap(i, j int) {
	f[i], f[j] = f[j], f[i]
}
