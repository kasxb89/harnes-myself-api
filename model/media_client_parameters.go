package model

// WithMediaClientConcurrencyHint copies the cached schema before adding a
// gateway operating ceiling. It neither reserves slots nor claims supplier capacity.
func WithMediaClientConcurrencyHint(parameters map[string]any, pluginKey, modelName, kind string) map[string]any {
	if parameters == nil {
		return nil
	}
	copy := make(map[string]any, len(parameters)+1)
	for key, value := range parameters {
		copy[key] = value
	}
	// A cached declaration must not survive if a route no longer reconciles.
	delete(copy, "x-client-concurrency")
	if kind != MediaImage && kind != MediaVideo && kind != MediaAudio {
		return copy
	}
	if parallelism := GetMediaClientConcurrencyHint(pluginKey, modelName, kind); parallelism != nil {
		copy["x-client-concurrency"] = map[string]any{"schema": 1, "atomic_admission": true, "max_parallelism": *parallelism, "model": modelName, "scope": "shared_account_pool", "reservation": false}
	}
	return copy
}
