package onlinestats

// From http://queue.acm.org/detail.cfm?id=2534976

import "math"

type ExpWeight struct {
	n     int
	m1    float64
	v     float64
	alpha float64
}

func NewExpWeight(alpha float64) *ExpWeight {
	return &ExpWeight{alpha: alpha}
}

func (e *ExpWeight) Push(x float64) {

	if e.n == 0 {
		e.m1 = x
		e.v = 1
	} else {
		e.m1 = (1-e.alpha)*x + e.alpha*e.m1
		e.v = (1-e.alpha)*(x-e.m1) + e.alpha*e.v
	}

	e.n++

}

func (e *ExpWeight) Len() int {
	return e.n
}

func (e *ExpWeight) Mean() float64 {
	return e.m1
}

func (e *ExpWeight) Var() float64 {
	return e.v
}

func (e *ExpWeight) Stddev() float64 {
	return math.Sqrt(e.v)
}
