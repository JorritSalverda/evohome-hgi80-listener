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

	deviation := int(0.25 * float64(input))

	return input - deviation + r.Intn(2*deviation)
}
