package main

import (
	"math/rand"
	"time"
)

func reverseMap(originalMap map[string]string) map[string]string {
	reverseMap := map[string]string{}

	for k, v := range originalMap {

		reverseMap[v] = k
	}

	return reverseMap
}

var r = rand.New(rand.NewSource(time.Now().UnixNano()))

func applyJitter(input int) (output int) {

	return applyJitterWithPercentage(input, 25)
}

func applyJitterWithPercentage(input, percentage int) (output int) {

	deviation := int(float64(input) * float64(percentage) / 100)

	return input - deviation + r.Intn(2*deviation)
}
