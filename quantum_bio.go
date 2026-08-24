package main

import (
	"flag"
	"fmt"
	"strings"
)

func addQuantumBiologyFlag() {
	flag.Bool("quantum-bio", false, "Enable quantum biology research search")
}

func handleQuantumBiologySearch() bool {
	f := flag.Lookup("quantum-bio")
	if f == nil || !f.Value.(flag.Getter).Get().(bool) {
		return false
	}

	fmt.Println("Starting quantum biology research search...")

	qbKeywords := []string{
		"quantum biology", "quantum coherence photosynthesis", "quantum tunneling enzyme",
		"avian magnetoreception", "quantum effects DNA", "cryptochrome", "quantum entanglement biology",
		"biophotons", "quantum brain", "quantum neuroscience",
	}
	keywords = append(keywords, qbKeywords...)
	keywords = removeDuplicates(keywords)
	seedKeywords = append(seedKeywords, qbKeywords...)
	allSeedKeywords = append(allSeedKeywords, qbKeywords...)

	seeds := []string{
		"https://www.quantumbiology.co.uk/",
		"https://phys.org/tags/quantum+biology/",
		"https://arxiv.org/search/?query=quantum+biology&searchtype=all",
		"https://www.nature.com/subjects/quantum-biology",
		"https://www.sciencedirect.com/search?query=quantum+biology",
		"https://journals.plos.org/plosone/search?q=quantum+biology",
	}
	kw := strings.Join(keywords, ",")
	for _, u := range seeds {
		addToQueue(u, 0, kw)
	}

	return true
}
