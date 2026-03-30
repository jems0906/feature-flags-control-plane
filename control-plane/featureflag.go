package main

// FeatureFlag represents a feature flag configuration.
type FeatureFlag struct {
	Name        string
	Enabled     bool
	Environment string // dev, stage, prod
	TargetRules []TargetRule
}

// Context for flag evaluation
type EvalContext struct {
	UserID   string
	TenantID string
	Headers  map[string]string
}

// IsFlagEnabled evaluates if the flag is enabled for the given context.
func (f *FeatureFlag) IsFlagEnabled(ctx EvalContext) bool {
	if !f.Enabled {
		return false
	}
	if len(f.TargetRules) == 0 {
		return true // No targeting rules, enabled for all
	}
	for _, rule := range f.TargetRules {
		switch rule.Type {
		case "user":
			if ctx.UserID != "" && ctx.UserID == rule.Value {
				return true
			}
		case "tenant":
			if ctx.TenantID != "" && ctx.TenantID == rule.Value {
				return true
			}
		case "header":
			if v, ok := ctx.Headers[rule.Value]; ok && v != "" {
				return true
			}
		case "percentage":
			if ctx.UserID != "" && rule.Rollout > 0 {
				h := hashString(ctx.UserID)
				if int(h%100) < rule.Rollout {
					return true
				}
			}
		}
	}
	return false
}

// hashString is a simple deterministic hash for bucketing
func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h = (h * 16777619) ^ uint32(s[i])
	}
	return h
}

// TargetRule defines targeting logic for a flag.
type TargetRule struct {
	Type    string // user, tenant, percentage, header
	Value   string
	Rollout int // percentage rollout
}
