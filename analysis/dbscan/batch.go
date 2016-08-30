package dbscan

import (
	"fmt"

	log "github.com/Sirupsen/logrus"

	"github.com/antongulenko/data2go/sample"
	"github.com/antongulenko/go-onlinestats"
)

type DbscanBatchClusterer struct {
	Dbscan

	TreeMinChildren int     // 25
	TreeMaxChildren int     // 50
	TreePointWidth  float64 // 0.0001
}

func (c *DbscanBatchClusterer) printSummary(clusters map[string][]*sample.Sample) {
	var stats onlinestats.Running
	for _, cluster := range clusters {
		stats.Push(float64(len(cluster)))
	}
	log.Printf("%v clusters, avg size %v, size stddev %v", len(clusters), stats.Mean(), stats.Stddev())
}

func (c *DbscanBatchClusterer) ProcessBatch(header *sample.Header, samples []*sample.Sample) (*sample.Header, []*sample.Sample, error) {
	log.Println("Building RTree...")

	tree := NewRtreeSetOfPoints(len(header.Fields), c.TreeMinChildren, c.TreeMaxChildren, c.TreePointWidth)
	for _, sample := range samples {
		tree.Add(sample)
	}

	log.Println("Clustering ...")
	clusters := tree.Cluster(&c.Dbscan)
	c.printSummary(clusters)
	return header, samples, nil
}

func (c *DbscanBatchClusterer) String() string {
	return fmt.Sprintf("Rtree-Dbscan(eps: %v, minpts: %v, tree: %v-%v, width: %v)",
		c.Eps, c.MinPts, c.TreeMinChildren, c.TreeMaxChildren, c.TreePointWidth)
}