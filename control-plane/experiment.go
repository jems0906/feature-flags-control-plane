package main

import "fmt"

// Experiment represents an A/B test configuration.
type Experiment struct {
	Name     string   `json:"Name"`
	Variants []string `json:"Variants"` // e.g., ["A", "B"]
}

// AssignVariant deterministically assigns a variant for a given userID using
// a stable hash so the same user always gets the same variant.
func (e *Experiment) AssignVariant(userID string) string {
	if len(e.Variants) == 0 {
		return ""
	}
	// Mix experiment name into hash so two experiments with same users split independently.
	h := hashString(userID + "|" + e.Name)
	idx := int(h) % len(e.Variants)
	variant := e.Variants[idx]
	VariantExposure.WithLabelValues(e.Name, variant).Inc()
	return variant
}

// RecordConversion records a conversion event for a variant in this experiment.
func (e *Experiment) RecordConversion(variant string) {
	ConversionEvents.WithLabelValues(e.Name, variant).Inc()
}

// String returns a human-readable representation.
func (e *Experiment) String() string {
	return fmt.Sprintf("Experiment{Name:%s, Variants:%v}", e.Name, e.Variants)
}
