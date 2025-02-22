package main

import (
	"time"
)

type DotV1 struct {
	d float64
}

type DotV1State float64

func NewDotV1(previousState map[string]any) DotV1 {
	for k, _ := range previousState {
		if k != "d" {
			panic("DotV1: unexpected state key '" + k + "'")
		}
	}
	return DotV1{
		d: previousState["d"].(float64),
	}
}

func NewEmptyDotV1() DotV1 {
	return DotV1{
		d: 0.0,
	}
}

func (d *DotV1) Value() float64 {
	return d.d
}

func (d *DotV1) Version() string {
	return "v1"
}

func (d *DotV1) Serialize() map[string]any {
	return map[string]any{
		"d": d.d,
	}
}

func (d *DotV1) TimePeriod() time.Duration {
	return 1 * time.Minute
}
func (d *DotV1) Debug() {
}

func (d *DotV1) Forward(timestamp time.Time, sentiments []string) error {
	_ = timestamp
	proportions := sentimentToProportionMap(sentiments)

	epsilon := 0.005 // a small value to increase/decrease the dot on each time step
	for _, proportion := range proportions {
		// NOTE this is a very special magical number whose tweaking leads to collapses on
		// either side of the dot spectrum (either everyone stays at 0 because no sentiment can breach the threshold,
		// or everyone's a 1 because a sentiment wins at every timestamp)
		if proportion > 0.405 {
			// the network is converging itself towards a common goal, increase dot by epsilon
			d.d = limitDot(d.d + epsilon)
			return nil
		}
	}

	// no convergence, decrease by epsilon
	d.d = limitDot(d.d - epsilon)
	return nil
}

func limitDot(d float64) float64 {
	if d > 1 {
		return 1
	}
	if d < 0 {
		return 0
	}
	return d
}
