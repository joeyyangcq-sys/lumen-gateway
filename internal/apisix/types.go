package apisix

import "encoding/json"

type Route struct {
	ID             ID              `json:"id"`
	URI            string          `json:"uri"`
	URIs           []string        `json:"uris"`
	Methods        []string        `json:"methods"`
	Hosts          []string        `json:"hosts"`
	Priority       int             `json:"priority"`
	Status         int             `json:"status"`
	ServiceID      ID              `json:"service_id"`
	UpstreamID     ID              `json:"upstream_id"`
	PluginConfigID ID              `json:"plugin_config_id"`
	Upstream       *Upstream       `json:"upstream"`
	Plugins        json.RawMessage `json:"plugins"`
}

type Service struct {
	ID             ID              `json:"id"`
	UpstreamID     ID              `json:"upstream_id"`
	PluginConfigID ID              `json:"plugin_config_id"`
	Upstream       *Upstream       `json:"upstream"`
	Plugins        json.RawMessage `json:"plugins"`
}

type Upstream struct {
	ID           ID              `json:"id"`
	Type         string          `json:"type"`
	Scheme       string          `json:"scheme"`
	PassHost     string          `json:"pass_host"`
	UpstreamHost string          `json:"upstream_host"`
	Nodes        json.RawMessage `json:"nodes"`
}

type PluginConfig struct {
	ID      ID              `json:"id"`
	Plugins json.RawMessage `json:"plugins"`
}
