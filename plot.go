package pipeline

import (
	"fmt"
	"os"
	"sync"

	log "github.com/Sirupsen/logrus"

	"github.com/antongulenko/data2go"
	"github.com/antongulenko/golib"
	"github.com/gonum/plot"
	"github.com/gonum/plot/plotutil"
	"github.com/gonum/plot/vg"
	"github.com/gonum/plot/vg/draw"
)

const (
	PlotWidth    = 20 * vg.Centimeter
	PlotHeight   = PlotWidth
	PlottedXAxis = 0
	PlottedYAxis = 1
)

func init() {
	plotutil.DefaultColors = append(plotutil.DefaultColors, plotutil.DarkColors...)
	plotutil.DefaultGlyphShapes = []draw.GlyphDrawer{
		draw.RingGlyph{},
		draw.SquareGlyph{},
		draw.TriangleGlyph{},
		draw.CrossGlyph{},
		draw.PlusGlyph{},
	}
}

type Plotter struct {
	AbstractProcessor
	OutputFile     string
	ColorTag       string
	SeparatePlots  bool // If true, every ColorTag value will create a new plot
	incomingHeader *data2go.Header
	data           map[string]PlotData
}

type PlotData []*data2go.Sample

func (data PlotData) Len() int {
	return len(data)
}

func (data PlotData) XY(i int) (x, y float64) {
	values := data[i].Values
	if len(values) == 0 {
		return 0, 0
	} else if len(values) == 1 {
		val := float64(values[PlottedXAxis])
		return val, val
	} else {
		return float64(values[PlottedXAxis]), float64(values[PlottedYAxis])
	}
}

func (p *Plotter) Header(header *data2go.Header) error {
	if err := p.CheckSink(); err != nil {
		return err
	} else {
		if len(header.Fields) == 0 {
			log.Warnln("Not receiving any metrics for plotting")
		} else if len(header.Fields) == 1 {
			log.Warnln("Plotting only 1 metrics with y=x")
		}
		p.incomingHeader = header
		p.data = make(map[string]PlotData)
		return p.OutgoingSink.Header(header)
	}
}

func (p *Plotter) Sample(sample *data2go.Sample, header *data2go.Header) error {
	if err := p.Check(sample, p.incomingHeader); err != nil {
		return err
	}
	p.plotSample(sample)
	return p.OutgoingSink.Sample(sample, header)
}

func (p *Plotter) plotSample(sample *data2go.Sample) {
	key := sample.Tag(p.ColorTag)
	if key == "" && p.ColorTag != "" {
		key = "(none)"
	}
	p.data[key] = append(p.data[key], sample)
}

func (p *Plotter) Start(wg *sync.WaitGroup) golib.StopChan {
	if file, err := os.Create(p.OutputFile); err != nil {
		// Check if file can be created to quickly fail
		return golib.TaskFinishedError(err)
	} else {
		_ = file.Close() // Drop error
	}
	return p.AbstractProcessor.Start(wg)
}

func (p *Plotter) Close() {
	var err error
	if p.SeparatePlots {
		_ = os.Remove(p.OutputFile) // Delete file created in Start(), drop error.
		err = p.saveSeparatePlots()
	} else {
		err = p.savePlot(p.data, nil, p.OutputFile)
	}
	if err != nil {
		p.Error(err)
	}
	p.CloseSink(nil)
}

func (p *Plotter) saveSeparatePlots() error {
	bounds, err := p.fillPlot(p.data, nil)
	if err != nil {
		return err
	}
	group := data2go.NewFileGroup(p.OutputFile)
	for name, data := range p.data {
		plotData := map[string]PlotData{name: data}
		plotFile := group.BuildFilenameStr(name)
		if err := p.savePlot(plotData, bounds, plotFile); err != nil {
			return err
		}
	}
	return nil
}

func (p *Plotter) savePlot(plotData map[string]PlotData, copyBounds *plot.Plot, targetFile string) error {
	plot, err := p.fillPlot(plotData, copyBounds)
	if err != nil {
		return err
	}
	return plot.Save(PlotWidth, PlotHeight, targetFile)
}

func (p *Plotter) fillPlot(plotData map[string]PlotData, copyBounds *plot.Plot) (*plot.Plot, error) {
	plot, err := plot.New()
	if err != nil {
		return nil, err
	}
	numFields := len(p.incomingHeader.Fields)
	if numFields >= 2 {
		plot.X.Label.Text = p.incomingHeader.Fields[PlottedXAxis]
		plot.Y.Label.Text = p.incomingHeader.Fields[PlottedYAxis]
	} else if numFields == 1 {
		plot.X.Label.Text = p.incomingHeader.Fields[PlottedXAxis]
		plot.Y.Label.Text = p.incomingHeader.Fields[PlottedXAxis]
	}
	if copyBounds != nil {
		plot.X.Min = copyBounds.X.Min
		plot.X.Max = copyBounds.X.Max
		plot.Y.Min = copyBounds.Y.Min
		plot.Y.Max = copyBounds.Y.Max
	}

	var parameters []interface{}
	for name, data := range plotData {
		parameters = append(parameters, name, data)
	}

	if err := plotutil.AddScatters(plot, parameters...); err != nil {
		return nil, fmt.Errorf("Error creating plot: %v", err)
	}
	return plot, nil
}

func (p *Plotter) String() string {
	return fmt.Sprintf("Plotter (color: %s)(file: %s)", p.ColorTag, p.OutputFile)
}