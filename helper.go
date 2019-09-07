package main

func reverseMap(originalMap map[string]string) map[string]string {
	reverseMap := map[string]string{}

	for k, v := range originalMap {

		reverseMap[v] = k
	}

	return reverseMap
}
