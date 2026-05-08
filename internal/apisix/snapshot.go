package apisix

type Snapshot struct {
	Routes       map[string]Route
	Services     map[string]Service
	Upstreams    map[string]Upstream
	PluginConfig map[string]PluginConfig
}

func NewSnapshot() Snapshot {
	return Snapshot{
		Routes:       make(map[string]Route),
		Services:     make(map[string]Service),
		Upstreams:    make(map[string]Upstream),
		PluginConfig: make(map[string]PluginConfig),
	}
}

