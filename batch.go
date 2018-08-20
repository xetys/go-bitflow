package pipeline

import (
	"fmt"
	"sync"
	"time"

	"github.com/antongulenko/go-bitflow"
	"github.com/antongulenko/golib"
	log "github.com/sirupsen/logrus"
)

type BatchProcessor struct {
	bitflow.NoopProcessor
	checker  bitflow.HeaderChecker
	samples  []*bitflow.Sample
	shutdown bool

	Steps []BatchProcessingStep

	FlushTimeout       time.Duration // If > 0, flush when no new samples are received for the given duration. The wall-time is used for this (not sample timestamps)
	lastAutoFlushError error
	lastSample         time.Time

	FlushTag     string // If set, flush every time this tag changes
	lastFlushTag string
	flushHeader  *bitflow.Header
	flushTrigger *golib.TimeoutCond // Used to trigger flush and to notify about finished flush. Relies on Sample()/Close() being synchronized externally.
	flushError   error
}

type BatchProcessingStep interface {
	ProcessBatch(header *bitflow.Header, samples []*bitflow.Sample) (*bitflow.Header, []*bitflow.Sample, error)
	String() string
}

type ResizingBatchProcessingStep interface {
	BatchProcessingStep
	OutputSampleSize(sampleSize int) int
}

func (p *BatchProcessor) OutputSampleSize(sampleSize int) int {
	for _, step := range p.Steps {
		if step, ok := step.(ResizingBatchProcessingStep); ok {
			sampleSize = step.OutputSampleSize(sampleSize)
		}
	}
	return sampleSize
}

func (p *BatchProcessor) Add(step BatchProcessingStep) *BatchProcessor {
	p.Steps = append(p.Steps, step)
	return p
}

func (p *BatchProcessor) ContainedStringers() []fmt.Stringer {
	res := make([]fmt.Stringer, len(p.Steps))
	for i, step := range p.Steps {
		res[i] = step
	}
	return res
}

func (p *BatchProcessor) Start(wg *sync.WaitGroup) golib.StopChan {
	p.flushTrigger = golib.NewTimeoutCond(new(sync.Mutex))
	wg.Add(1)
	go p.loopFlush(wg)
	return p.NoopProcessor.Start(wg)
}

func (p *BatchProcessor) Sample(sample *bitflow.Sample, header *bitflow.Header) (err error) {
	oldHeader := p.checker.LastHeader
	flush := p.checker.InitializedHeaderChanged(header)
	if p.FlushTag != "" {
		val := sample.Tag(p.FlushTag)
		if oldHeader != nil {
			flush = flush || val != p.lastFlushTag
		}
		p.lastFlushTag = val
	}
	if flush {
		err = p.triggerFlush(oldHeader, false)
	}
	if p.FlushTimeout > 0 {
		p.lastSample = time.Now()
		if err == nil {
			err = p.lastAutoFlushError
		}
		p.lastAutoFlushError = nil
	}
	p.samples = append(p.samples, sample)
	return
}

func (p *BatchProcessor) Close() {
	defer p.NoopProcessor.Close()
	header := p.checker.LastHeader
	if header == nil {
		log.Warnln(p.String(), "received no samples")
	}
	if err := p.triggerFlush(header, true); err != nil {
		p.Error(err)
	}
}

func (p *BatchProcessor) triggerFlush(header *bitflow.Header, shutdown bool) error {
	p.flushTrigger.L.Lock()
	defer p.flushTrigger.L.Unlock()
	p.flushHeader = header
	p.flushTrigger.Broadcast()
	p.shutdown = shutdown
	for p.flushHeader != nil {
		p.flushTrigger.Wait() // Will be notified after flush is finished
	}
	res := p.flushError
	p.flushError = nil
	return res
}

func (p *BatchProcessor) loopFlush(wg *sync.WaitGroup) {
	defer wg.Done()
	for p.waitAndExecuteFlush() {
	}
}

func (p *BatchProcessor) waitAndExecuteFlush() bool {
	p.flushTrigger.L.Lock()
	defer p.flushTrigger.L.Unlock()
	for p.flushHeader == nil && !p.shutdown && !p.flushTimedOut() {
		if p.FlushTimeout > 0 {
			p.flushTrigger.WaitTimeout(p.FlushTimeout)
		} else {
			p.flushTrigger.Wait()
		}
	}
	if p.flushHeader == nil && !p.shutdown {
		// Automatic flush after timeout
		err := p.executeFlush(p.checker.LastHeader)
		if err != nil {
			log.Errorf("%v: Error during automatic flush (will be returned when next sample arrives): %v", p, err)
			p.lastAutoFlushError = fmt.Errorf("Error during previous auto-flush: %v", err)
		}
		p.lastSample = time.Now()
	} else {
		p.flushError = p.executeFlush(p.flushHeader)
		p.flushTrigger.Broadcast()
	}
	p.flushHeader = nil
	return !p.shutdown
}

func (p *BatchProcessor) flushTimedOut() bool {
	if p.FlushTimeout <= 0 || p.lastSample.IsZero() {
		return false
	}
	return time.Now().Sub(p.lastSample) >= p.FlushTimeout
}

func (p *BatchProcessor) executeFlush(header *bitflow.Header) error {
	samples := p.samples
	if len(samples) == 0 || header == nil {
		return nil
	}
	p.samples = nil // Allow garbage collection
	if samples, header, err := p.executeSteps(samples, header); err != nil {
		return err
	} else {
		if header == nil {
			return fmt.Errorf("Cannot flush %v samples because nil-header was returned by last batch processing step", len(samples))
		}
		log.Println("Flushing", len(samples), "batched samples with", len(header.Fields), "metrics")
		for _, sample := range samples {
			if err := p.NoopProcessor.Sample(sample, header); err != nil {
				return fmt.Errorf("Error flushing batch: %v", err)
			}
		}
		return nil
	}
}

func (p *BatchProcessor) executeSteps(samples []*bitflow.Sample, header *bitflow.Header) ([]*bitflow.Sample, *bitflow.Header, error) {
	if len(p.Steps) > 0 {
		log.Println("Executing", len(p.Steps), "batch processing step(s)")
		for i, step := range p.Steps {
			if len(samples) == 0 {
				log.Warnln("Cannot execute remaining", len(p.Steps)-i, "batch step(s) because the batch with", len(header.Fields), "has no samples")
				break
			} else {
				log.Println("Executing", step, "on", len(samples), "samples with", len(header.Fields), "metrics")
				var err error
				header, samples, err = step.ProcessBatch(header, samples)
				if err != nil {
					return nil, nil, err
				}
			}
		}
	}
	return samples, header, nil
}

func (p *BatchProcessor) String() string {
	extra := "s"
	if len(p.Steps) == 1 {
		extra = ""
	}
	flushed := ""
	if p.FlushTag != "" {
		flushed = fmt.Sprintf(", flushed with tag '%v'", p.FlushTag)
	}
	if p.FlushTimeout > 0 {
		flushed += fmt.Sprintf(", auto-flushed after %v", p.FlushTimeout)
	}
	return fmt.Sprintf("BatchProcessor (%v step%s%s)", len(p.Steps), extra, flushed)
}

func (p *BatchProcessor) MergeProcessor(other bitflow.SampleProcessor) bool {
	if otherBatch, ok := other.(*BatchProcessor); !ok {
		return false
	} else {
		p.Steps = append(p.Steps, otherBatch.Steps...)
		return true
	}
}

// ==================== Simple implementation ====================

type SimpleBatchProcessingStep struct {
	Description          string
	Process              func(header *bitflow.Header, samples []*bitflow.Sample) (*bitflow.Header, []*bitflow.Sample, error)
	OutputSampleSizeFunc func(sampleSize int) int
}

func (s *SimpleBatchProcessingStep) ProcessBatch(header *bitflow.Header, samples []*bitflow.Sample) (*bitflow.Header, []*bitflow.Sample, error) {
	if process := s.Process; process == nil {
		return nil, nil, fmt.Errorf("%v: Process function is not set", s)
	} else {
		return process(header, samples)
	}
}

func (s *SimpleBatchProcessingStep) String() string {
	if s.Description == "" {
		return "SimpleBatchProcessingStep"
	} else {
		return s.Description
	}
}

func (s *SimpleBatchProcessingStep) OutputSampleSize(sampleSize int) int {
	if f := s.OutputSampleSizeFunc; f != nil {
		return f(sampleSize)
	}
	return sampleSize
}
