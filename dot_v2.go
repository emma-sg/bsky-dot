package main

import (
	"fmt"
	"time"

	"github.com/samber/lo"
)

type DotV2Sentiment struct {
	Timestamp int64  `json:"t"`
	Value     string `json:"v"`
}

type DotV2 struct {
	D              float64          `json:"d"`
	LastSentiments []DotV2Sentiment `json:"s"`
}

const dotV2SentimentSize = 40

func NewDotV2(previousState map[string]any) DotV2 {
	sentimentsStrArray := make([]DotV2Sentiment, 0)

	for _, sentimentAny := range previousState["s"].([]any) {
		sentimentMap := sentimentAny.(map[string]any)
		sentimentsStrArray = append(sentimentsStrArray, DotV2Sentiment{
			Timestamp: int64(sentimentMap["t"].(float64)),
			Value:     sentimentMap["v"].(string),
		})
	}
	if len(sentimentsStrArray) > dotV2SentimentSize {
		sentimentsStrArray = sentimentsStrArray[:dotV2SentimentSize]
	}
	return DotV2{
		D:              previousState["d"].(float64),
		LastSentiments: sentimentsStrArray,
	}
}

func NewEmptyDotV2() DotV2 {
	return DotV2{
		D:              0.0,
		LastSentiments: make([]DotV2Sentiment, 0),
	}
}

func (d *DotV2) Serialize() map[string]any {
	asSentimentMap := make([]map[string]any, 0)
	for _, s := range d.LastSentiments {
		asSentimentMap = append(asSentimentMap, map[string]any{
			"t": float64(s.Timestamp),
			"v": s.Value,
		})
	}
	return map[string]any{
		"d": d.D,
		"s": lo.ToAnySlice(asSentimentMap),
	}
}

func (d *DotV2) TimePeriod() time.Duration {
	return 1 * time.Minute
}
func (d *DotV2) Version() string {
	return "v2"
}

func appendToMaxLen(array []DotV2Sentiment, maxLen int, value DotV2Sentiment) []DotV2Sentiment {
	if len(array) >= maxLen {
		array = array[1:]
	}
	array = append(array, value)
	if len(array) > maxLen {
		panic("array is too long")
	}
	return array
}

func (d *DotV2) Value() float64 {
	return d.D
}

func (d *DotV2) lastWinningSentiment(timestamp time.Time) string {

	winCounter := make(map[string]int)
	for _, lastSentiment := range d.LastSentiments {
		delta := timestamp.Unix() - lastSentiment.Timestamp
		if delta > 20*60 {
			continue
		}
		winCounter[lastSentiment.Value]++
	}

	var winningSentiment string
	var winningCount int
	for sentiment, count := range winCounter {
		if count > winningCount {
			winningSentiment = sentiment
			winningCount = count
		}
	}

	return winningSentiment
}

func sentimentToProportionMap(sentiments []string) map[string]float64 {
	counters := make(map[string]uint)
	for _, sentiment := range sentiments {
		counters[sentiment]++
	}
	proportions := make(map[string]float64)
	for sentiment, count := range counters {
		proportion := float64(count) / float64(len(sentiments))
		proportions[sentiment] = proportion
	}
	return proportions
}

func (d *DotV2) Debug() {
	for idx, sentiment := range d.LastSentiments {
		fmt.Printf("%v %v %v\n", idx, sentiment.Timestamp, sentiment.Value)
	}
	fmt.Printf("\n")
}

func (d *DotV2) Forward(timestamp time.Time, sentiments []string) error {
	proportions := sentimentToProportionMap(sentiments)

	epsilonIncrease := 0.06
	epsilonDecrease := 0.009
	//var winningSentiment string
	//for sentiment, proportion := range proportions {
	//	if proportion > 0.33 {
	//		winningSentiment = sentiment
	//	}
	//}
	emotions := (proportions["positive"] + proportions["negative"])

	if emotions > proportions["neutral"] {
		d.D = limitDot(d.D + (epsilonIncrease))
		return nil
	} else {
		// no convergence, decrease by epsilon
		d.D = limitDot(d.D - epsilonDecrease)
		return nil
	}
}
