package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Options struct {
	Gateway GatewayOptions `yaml:"gateway"`
	Admin   AdminOptions   `yaml:"admin"`
	Etcd    EtcdOptions    `yaml:"etcd"`
	File    FileOptions    `yaml:"file"`
}

type GatewayOptions struct {
	Listen string `yaml:"listen"`
	Source string `yaml:"source"`
}

type AdminOptions struct {
	Key string `yaml:"key"`
}

type EtcdOptions struct {
	Endpoints   []string      `yaml:"endpoints"`
	Prefix      string        `yaml:"prefix"`
	DialTimeout time.Duration `yaml:"dial_timeout"`
	Username    string        `yaml:"username"`
	Password    string        `yaml:"password"`
}

type FileOptions struct {
	Path string `yaml:"path"`
}

func Load(path string) (Options, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Options{}, err
	}

	var options Options
	if err := yaml.Unmarshal(data, &options); err != nil {
		return Options{}, err
	}

	options.applyDefaults()
	if err := options.Validate(); err != nil {
		return Options{}, err
	}

	return options, nil
}

func (o *Options) applyDefaults() {
	if o.Gateway.Listen == "" {
		o.Gateway.Listen = ":18080"
	}
	if o.Gateway.Source == "" {
		o.Gateway.Source = "file"
	}
	if o.Admin.Key == "" {
		o.Admin.Key = "local-dev-admin-key"
	}
	if o.Etcd.Prefix == "" {
		o.Etcd.Prefix = "/apisix"
	}
	if o.Etcd.DialTimeout == 0 {
		o.Etcd.DialTimeout = 3 * time.Second
	}
	if o.File.Path == "" {
		o.File.Path = "configs/lumen.yaml"
	}
}

func (o Options) Validate() error {
	if o.Gateway.Listen == "" {
		return errors.New("gateway.listen cannot be empty")
	}

	switch o.Gateway.Source {
	case "file":
		if o.File.Path == "" {
			return errors.New("file.path cannot be empty when gateway.source=file")
		}
	case "etcd_apisix":
		if len(o.Etcd.Endpoints) == 0 {
			return errors.New("etcd.endpoints cannot be empty when gateway.source=etcd_apisix")
		}
		if o.Etcd.Prefix == "" {
			return errors.New("etcd.prefix cannot be empty when gateway.source=etcd_apisix")
		}
	default:
		return fmt.Errorf("unsupported gateway.source %q", o.Gateway.Source)
	}

	return nil
}
