package analysis

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	log "github.com/Sirupsen/logrus"

	"github.com/antongulenko/data2go"
	"github.com/gonum/matrix/mat64"
	"github.com/gonum/stat"
)

const DefaultContainedVariance = 0.99

func SamplesToMatrix(samples []*data2go.Sample) mat64.Matrix {
	if len(samples) < 1 {
		return mat64.NewDense(0, 0, nil)
	}
	cols := len(samples[0].Values)
	values := make([]float64, len(samples)*cols)
	index := 0
	for _, sample := range samples {
		for _, val := range sample.Values {
			values[index] = float64(val)
			index++
		}
	}
	return mat64.NewDense(len(samples), cols, values)
}

func SampleToVector(sample *data2go.Sample) []float64 {
	input := sample.Values
	values := make([]float64, len(input))
	for i, val := range input {
		values[i] = float64(val)
	}
	return values
}

func SetSampleValues(s *data2go.Sample, values []float64) {
	if len(s.Values) >= len(values) {
		s.Values = s.Values[:len(values)]
	} else {
		s.Values = make([]data2go.Value, len(values))
	}
	for i, val := range values {
		s.Values[i] = data2go.Value(val)
	}
}

type PCAModel struct {
	Vectors            *mat64.Dense
	RawVariances       []float64
	ContainedVariances []float64
}

func (model *PCAModel) ComputeModel(samples []*data2go.Sample) error {
	matrix := SamplesToMatrix(samples)
	var ok bool
	model.Vectors, model.RawVariances, ok = stat.PrincipalComponents(matrix, nil)
	model.ContainedVariances = make([]float64, len(model.RawVariances))
	var sum float64
	for _, variance := range model.RawVariances {
		sum += variance
	}
	for i, variance := range model.RawVariances {
		model.ContainedVariances[i] = variance / sum
	}
	if !ok {
		return errors.New("PCA model could not be computed")
	}
	return nil
}

func (model *PCAModel) ComponentsContainingVariance(variance float64) (count int, sum float64) {
	for _, contained := range model.ContainedVariances {
		sum += contained
		count++
		if sum > variance {
			break
		}
	}
	return
}

func (model *PCAModel) String() string {
	return model.Report(DefaultContainedVariance)
}

func (model *PCAModel) Report(reportVariance float64) string {
	totalComponents := len(model.ContainedVariances)
	if model.Vectors == nil || totalComponents == 0 {
		return "PCA model (empty)"
	}
	var buf bytes.Buffer
	num, variance := model.ComponentsContainingVariance(reportVariance)
	fmt.Fprintf(&buf, "PCA model (%v total components, %v components contain %.4f variance): ", totalComponents, num, variance)
	fmt.Fprintf(&buf, "%.4f", model.ContainedVariances[:num])
	return buf.String()
}

type PCAProjection struct {
	Model      *PCAModel
	Vectors    mat64.Matrix
	Components int
}

func (model *PCAModel) Project(numComponents int) *PCAProjection {
	vectors := model.Vectors.View(0, 0, len(model.ContainedVariances), numComponents)
	return &PCAProjection{
		Model:      model,
		Vectors:    vectors,
		Components: numComponents,
	}
}

func (model *PCAProjection) Matrix(matrix mat64.Matrix) *mat64.Dense {
	var result mat64.Dense
	result.Mul(matrix, model.Vectors)
	return &result
}

func (model *PCAProjection) Vector(vec []float64) []float64 {
	matrix := model.Matrix(mat64.NewDense(1, len(vec), vec))
	return matrix.RawRowView(0)
}

func (model *PCAProjection) Sample(sample *data2go.Sample) (result *data2go.Sample) {
	values := model.Vector(SampleToVector(sample))
	SetSampleValues(result, values)
	result.CopyMetadataFrom(sample)
	return
}

type PCABatchProcessing struct {
	ContainedVariance float64
}

func (p *PCABatchProcessing) ProcessBatch(header *data2go.Header, samples []*data2go.Sample) (*data2go.Header, []*data2go.Sample, error) {
	variance := p.ContainedVariance
	if variance <= 0 {
		variance = DefaultContainedVariance
	}
	log.Println("Computing PCA model...")
	var model PCAModel
	if err := model.ComputeModel(samples); err != nil {
		outErr := fmt.Errorf("Error in %v: %v", p, err)
		log.Errorln(outErr)
		return header, nil, outErr
	}
	comp, variance := model.ComponentsContainingVariance(variance)
	log.Println(model.Report(DefaultContainedVariance))
	log.Printf("Projecting data into %v components (variance %.4f)...", comp, variance)
	proj := model.Project(comp)

	// TODO this could be done in one matrix
	outputSamples := make([]*data2go.Sample, len(samples))
	for i, inputSample := range samples {
		outputSample := proj.Sample(inputSample)
		outputSamples[i] = outputSample
	}

	outFields := make([]string, comp)
	for i := 0; i < comp; i++ {
		outFields[i] = "component" + strconv.Itoa(i)
	}
	outHeader := &data2go.Header{
		HasTags: header.HasTags,
		Fields:  outFields,
	}
	return outHeader, outputSamples, nil
}

func (p *PCABatchProcessing) String() string {
	return fmt.Sprintf("PCA batch processing (%v variance)", p.ContainedVariance)
}