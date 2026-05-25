package lumen

import (
	"errors"
	"os"
	"testing"

	publicbalancer "github.com/joey/lumen-gateway/balancer"
	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/controlplane"
	"github.com/joey/lumen-gateway/internal/gateway"
	publicplugin "github.com/joey/lumen-gateway/plugin"
	"github.com/urfave/cli/v2"
)

func TestLumenOptionsAndRun(t *testing.T) {
	// 备份并恢复 os.Args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// 1. 测试 Options builder
	opt := &options{}
	WithVersion("1.2.3")(opt)
	if opt.version != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %q", opt.version)
	}

	WithFlags(&cli.StringFlag{Name: "custom"})(opt)
	if len(opt.flags) != 1 {
		t.Errorf("expected 1 custom flag, got %d", len(opt.flags))
	}

	initFn := func(b bootstrap.Options) error { return nil }
	WithInit(initFn)(opt)
	if opt.init == nil {
		t.Error("expected init function to be set")
	}

	WithPlugins(nil)(opt)
	if len(opt.pluginRegs) != 1 {
		t.Error("expected 1 plugin registrar")
	}

	WithBalancerType("custom_rr", nil)(opt)
	if _, ok := opt.balancerTypes["custom_rr"]; !ok {
		t.Error("expected custom balancer type to be registered")
	}

	WithCompilerOptions(nil)(opt)
	if len(opt.compilerOpts) != 1 {
		t.Error("expected 1 compiler option")
	}

	// 2. 创建临时有效的 bootstrap 配置文件
	tmpFile, err := os.CreateTemp("", "bootstrap-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(`
gateway:
  listen: :18080
  source: file
file:
  path: configs/lumen.yaml
`)
	_ = tmpFile.Close()

	// 3. 测试 Run (正常逻辑下的 init 报错，中断执行，避免启动服务器)
	os.Args = []string{"lumen-gateway", "--config", tmpFile.Name()}
	abortErr := errors.New("abort init")
	err = Run(
		WithInit(func(boot bootstrap.Options) error {
			return abortErr
		}),
	)
	if !errors.Is(err, abortErr) {
		t.Fatalf("expected abort init error, got %v", err)
	}

	// 4. 测试 Run (带有 --test 标志，直接退出且 init 正常运行)
	os.Args = []string{"lumen-gateway", "--config", tmpFile.Name(), "--test"}
	initCalled := false
	err = Run(
		WithInit(func(boot bootstrap.Options) error {
			initCalled = true
			return nil
		}),
	)
	if err != nil {
		t.Errorf("expected no error with --test, got %v", err)
	}
	// 注意：在 Run 内部，若 ctx.Bool("test") 为真，会直接在 Run Action 的第205行打印并返回，不会执行 init。
	// 因此 initCalled 保持 false 是符合预期的行为。
	if initCalled {
		t.Error("expected init not to be called when --test is provided")
	}

	// 5. 测试加载不存在的配置报错路径
	os.Args = []string{"lumen-gateway", "--config", "nonexistent_config_file_path.yaml"}
	err = Run()
	if err == nil {
		t.Error("expected error when config file does not exist")
	}
}

func TestRunErrorPaths(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// 1. 测试 bootstrap.Load 遇到无效的 source 报错
	tmpFile1, err := os.CreateTemp("", "bootstrap-err-source-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile1.Name())
	_, _ = tmpFile1.WriteString(`
gateway:
  listen: :18080
  source: invalid_source
`)
	_ = tmpFile1.Close()

	os.Args = []string{"lumen-gateway", "--config", tmpFile1.Name()}
	err = Run()
	if err == nil {
		t.Error("expected error when gateway source is invalid in bootstrap config")
	}

	// 2. 测试 source.Load 报错（file.path nonexistent_lumen.yaml 不存在）
	tmpFile3, err := os.CreateTemp("", "bootstrap-err-load-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile3.Name())
	_, _ = tmpFile3.WriteString(`
gateway:
  listen: :18080
  source: file
file:
  path: nonexistent_lumen.yaml
`)
	_ = tmpFile3.Close()

	os.Args = []string{"lumen-gateway", "--config", tmpFile3.Name()}
	err = Run()
	if err == nil {
		t.Error("expected error when file source loading fails due to nonexistent file")
	}
}

func TestBuildCompilerOpts(t *testing.T) {
	opt := &options{}

	// 成功和失败的 plugin 注册函数
	successReg := func(r *publicplugin.Registry) error {
		return nil
	}
	failReg := func(r *publicplugin.Registry) error {
		return errors.New("registrar error")
	}

	// 自定义负载均衡器工厂
	customBalancer := func(endpoints []publicbalancer.Endpoint, params any) (publicbalancer.Balancer, error) {
		return nil, nil
	}

	WithPlugins(successReg)(opt)
	WithBalancerType("custom_rr", customBalancer)(opt)

	compilerOpts := opt.buildCompilerOpts()
	if len(compilerOpts) != 2 {
		t.Fatalf("expected 2 compiler options, got %d", len(compilerOpts))
	}

	// 构造公有 Compiler 并应用 options 验证闭包
	c := gateway.NewRuntimeCompiler(compilerOpts...)

	// 验证 RegistryFactory 闭包成功执行，可通过 BuildRegistry 触发
	r, err := c.BuildRegistry()
	if err != nil {
		t.Errorf("expected no error building registry, got %v", err)
	}
	if r == nil {
		t.Error("expected registry to be non-nil")
	}

	// 验证 RegistryFactory 闭包遇到错误时的情况
	optFail := &options{}
	WithPlugins(failReg)(optFail)
	cFail := gateway.NewRuntimeCompiler(optFail.buildCompilerOpts()...)
	_, err = cFail.Compile(config.Options{})
	if err == nil {
		t.Error("expected error from failed plugin registrar during Compile")
	}

	// 验证 BalancerFactory 闭包的各种分支
	// 1. 自定义负载均衡器
	optsCustom := config.Options{
		Servers: map[string]config.ServerOptions{
			"srv1": {ID: "srv1"},
		},
		Upstreams: map[string]config.UpstreamOptions{
			"up1": {
				Balancer: config.BalancerOptions{Type: "custom_rr"},
				Endpoints: []config.EndpointOptions{
					{Address: "127.0.0.1:8080", Weight: 1},
				},
			},
		},
	}
	_, err = c.Compile(optsCustom)
	if err != nil {
		t.Errorf("expected no error compiling custom balancer option, got %v", err)
	}

	// 2. 内置 round_robin
	optsRoundRobin := config.Options{
		Servers: map[string]config.ServerOptions{
			"srv1": {ID: "srv1"},
		},
		Upstreams: map[string]config.UpstreamOptions{
			"up1": {
				Balancer: config.BalancerOptions{Type: "round_robin"},
				Endpoints: []config.EndpointOptions{
					{Address: "127.0.0.1:8080", Weight: 1},
				},
			},
		},
	}
	_, err = c.Compile(optsRoundRobin)
	if err != nil {
		t.Errorf("expected no error compiling round_robin balancer, got %v", err)
	}

	// 3. 不支持的负载均衡器类型
	optsUnsupported := config.Options{
		Servers: map[string]config.ServerOptions{
			"srv1": {ID: "srv1"},
		},
		Upstreams: map[string]config.UpstreamOptions{
			"up1": {
				Balancer: config.BalancerOptions{Type: "invalid_type"},
				Endpoints: []config.EndpointOptions{
					{Address: "127.0.0.1:8080", Weight: 1},
				},
			},
		},
	}
	_, err = c.Compile(optsUnsupported)
	if err == nil {
		t.Error("expected error for unsupported balancer type")
	}
}

func TestAdminCommandsExecution(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// 1. 创建临时的且 endpoints 为空的配置文件，用以让 NewEtcdStore 报错
	tmpFile1, err := os.CreateTemp("", "bootstrap-admin-etcd-err-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile1.Name())
	_, _ = tmpFile1.WriteString(`
gateway:
  listen: :18080
  source: file
etcd:
  endpoints: []
`)
	_ = tmpFile1.Close()

	os.Args = []string{"lumen-gateway", "--config", tmpFile1.Name(), "admin", "import", "--file", "dummy.bundle"}
	err = Run()
	if err == nil {
		t.Error("expected error due to empty etcd endpoints")
	}

	// 2. 创建包含有效 etcd endpoints 配置但 gateway.source=file 的配置文件
	tmpFile2, err := os.CreateTemp("", "bootstrap-admin-source-err-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile2.Name())
	_, _ = tmpFile2.WriteString(`
gateway:
  listen: :18080
  source: file
etcd:
  endpoints:
    - 127.0.0.1:2379
`)
	_ = tmpFile2.Close()

	// 验证 admin import
	os.Args = []string{"lumen-gateway", "--config", tmpFile2.Name(), "admin", "import", "--file", "dummy.bundle"}
	err = Run()
	if err == nil || err.Error() != "admin import requires gateway.source=etcd_apisix" {
		t.Errorf("expected source error, got %v", err)
	}

	// 验证 admin export
	os.Args = []string{"lumen-gateway", "--config", tmpFile2.Name(), "admin", "export", "--out", "dummy.bundle"}
	err = Run()
	if err == nil || err.Error() != "admin export requires gateway.source=etcd_apisix" {
		t.Errorf("expected source error, got %v", err)
	}

	// 验证 admin sync
	os.Args = []string{"lumen-gateway", "--config", tmpFile2.Name(), "admin", "sync", "--file", "dummy.bundle"}
	err = Run()
	if err == nil || err.Error() != "admin sync requires gateway.source=etcd_apisix" {
		t.Errorf("expected source error, got %v", err)
	}

	// 验证 admin sync --watch
	os.Args = []string{"lumen-gateway", "--config", tmpFile2.Name(), "admin", "sync", "--file", "dummy.bundle", "--watch"}
	err = Run()
	if err == nil || err.Error() != "admin sync requires gateway.source=etcd_apisix" {
		t.Errorf("expected source error, got %v", err)
	}
}

func TestPrivateHelperMethods(t *testing.T) {
	// 1. 测试 parseKinds
	kinds, err := parseKinds(nil)
	if err != nil || kinds != nil {
		t.Errorf("expected nil, nil for parseKinds(nil), got %v, %v", kinds, err)
	}

	kinds, err = parseKinds([]string{"routes", "services", "upstreams"})
	if err != nil {
		t.Errorf("unexpected error for parseKinds: %v", err)
	}
	if len(kinds) != 3 {
		t.Errorf("expected 3 kinds, got %d", len(kinds))
	}

	_, err = parseKinds([]string{"invalid_kind"})
	if err == nil {
		t.Error("expected error for invalid resource kind")
	}

	// 2. 测试 printApplyResult (验证无崩溃)
	printApplyResult(controlplane.ApplyResult{
		Counts: map[controlplane.ResourceKind]int{
			controlplane.KindRoute: 5,
		},
	})
}

func TestVersionFlag(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"lumen-gateway", "--version"}
	err := Run()
	if err != nil {
		t.Errorf("expected no error with --version, got %v", err)
	}
}

func TestRunEtcdApisixSourcePath(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	tmpFile, err := os.CreateTemp("", "bootstrap-etcd-path-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(`
gateway:
  listen: :18080
  source: etcd_apisix
etcd:
  endpoints:
    - nonexistent.etcd.local:2379
  dial_timeout: 1ms
`)
	_ = tmpFile.Close()

	os.Args = []string{"lumen-gateway", "--config", tmpFile.Name()}
	err = Run()
	if err == nil {
		t.Error("expected error due to etcd loading failure/timeout")
	}
}

