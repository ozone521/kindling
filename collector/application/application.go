package application

import (
	"flag"
	"fmt"
	"github.com/dxsup/kindling-collector/analyzer"
	"github.com/dxsup/kindling-collector/analyzer/network"
	"github.com/dxsup/kindling-collector/analyzer/tcpmetricanalyzer"
	"github.com/dxsup/kindling-collector/analyzer/uprobeanalyzer"
	"github.com/dxsup/kindling-collector/consumer"
	"github.com/dxsup/kindling-collector/consumer/exporter/otelexporter"
	"github.com/dxsup/kindling-collector/consumer/processor/k8sprocessor"
	"github.com/dxsup/kindling-collector/consumer/processor/kindlingformatprocessor"
	"github.com/dxsup/kindling-collector/consumer/processor/nodemetricprocessor"
	"github.com/dxsup/kindling-collector/receiver"
	"github.com/dxsup/kindling-collector/receiver/udsreceiver"
	"github.com/spf13/viper"
	"go.uber.org/multierr"
)

type Application struct {
	viper             *viper.Viper
	componentsFactory *ComponentsFactory
	receiver          receiver.Receiver
	analyzerManager   analyzer.Manager
}

func New() (*Application, error) {
	app := &Application{
		viper:             viper.New(),
		componentsFactory: NewComponentsFactory(),
	}
	app.registerFactory()
	// Initialize flags
	configPath := flag.String("config", "kindling-collector-config.yml", "Configuration file")
	flag.Parse()
	err := app.readConfig(*configPath)
	if err != nil {
		return nil, fmt.Errorf("fail to read configuration: %w", err)
	}
	// Build processing pipeline
	err = app.buildPipeline()
	if err != nil {
		return nil, fmt.Errorf("failed to build pipeline: %w", err)
	}
	return app, nil
}

func (a *Application) Run() error {
	err := a.analyzerManager.StartAll(a.componentsFactory.logger)
	if err != nil {
		return fmt.Errorf("failed to start application: %v", err)
	}
	// Wait until the receiver shutdowns
	err = a.receiver.Start()
	if err != nil {
		return fmt.Errorf("failed to start application: %v", err)
	}
	return nil
}

func (a *Application) Shutdown() error {
	return multierr.Combine(a.receiver.Shutdown(), a.analyzerManager.ShutdownAll(a.componentsFactory.logger))
}

func initFlags() error {
	return nil
}

func (a *Application) registerFactory() {
	a.componentsFactory.RegisterReceiver(udsreceiver.Uds, udsreceiver.NewUdsReceiver, &udsreceiver.Config{})
	a.componentsFactory.RegisterAnalyzer(network.Network.String(), network.NewNetworkAnalyzer, &network.Config{})
	a.componentsFactory.RegisterProcessor(k8sprocessor.K8sMetadata, k8sprocessor.NewKubernetesProcessor, &k8sprocessor.Config{})
	a.componentsFactory.RegisterProcessor(kindlingformatprocessor.ProcessorName, kindlingformatprocessor.NewRelabelProcessor, &kindlingformatprocessor.Config{})
	a.componentsFactory.RegisterExporter(otelexporter.Otel, otelexporter.NewExporter, &otelexporter.Config{})
	a.componentsFactory.RegisterAnalyzer(tcpmetricanalyzer.TcpMetric.String(), tcpmetricanalyzer.NewTcpMetricAnalyzer, &tcpmetricanalyzer.Config{})
	a.componentsFactory.RegisterAnalyzer(uprobeanalyzer.UprobeType.String(), uprobeanalyzer.NewUprobeAnalyzer, &uprobeanalyzer.Config{})
	a.componentsFactory.RegisterProcessor(nodemetricprocessor.Type, nodemetricprocessor.New, &nodemetricprocessor.Config{})
}

func (a *Application) readConfig(path string) error {
	a.viper.SetConfigFile(path)
	err := a.viper.ReadInConfig()
	if err != nil { // Handle errors reading the config file
		return fmt.Errorf("error happened while reading config file: %w", err)
	}
	err = a.componentsFactory.ConstructConfig(a.viper)
	if err != nil {
		return fmt.Errorf("error happened while constructing config: %w", err)
	}
	return nil
}

// buildPipeline builds a event processing pipeline based on hard-code.
func (a *Application) buildPipeline() error {
	// TODO: Build pipeline via configuration to implement dependency injection
	// Initialize exporters
	otelExporterFactory := a.componentsFactory.Exporters[otelexporter.Otel]
	otelExporter := otelExporterFactory.newFunc(otelExporterFactory.config, a.componentsFactory.logger)
	// Initialize all processors
	// 1. Kindling Metric Format Processor
	formatProcessorFactory := a.componentsFactory.Processors[kindlingformatprocessor.ProcessorName]
	formatProcessor := formatProcessorFactory.newFunc(formatProcessorFactory.config, a.componentsFactory.logger, otelExporter)
	// 2. Kubernetes metadata processor
	k8sProcessorFactory := a.componentsFactory.Processors[k8sprocessor.K8sMetadata]
	k8sMetadataProcessor := k8sProcessorFactory.newFunc(k8sProcessorFactory.config, a.componentsFactory.logger, formatProcessor)
	// 3. Node Metric Generating Processor
	nodeMetricProcessorFactory := a.componentsFactory.Processors[nodemetricprocessor.Type]
	nodeMetricProcessor := nodeMetricProcessorFactory.newFunc(nodeMetricProcessorFactory.config, a.componentsFactory.logger, otelExporter)
	k8sMetadataProcessorForNodeMetric := k8sProcessorFactory.newFunc(k8sProcessorFactory.config, a.componentsFactory.logger, nodeMetricProcessor)
	// Initialize all analyzers
	// 1. Common network request analyzer
	networkAnalyzerFactory := a.componentsFactory.Analyzers[network.Network.String()]
	networkAnalyzer := networkAnalyzerFactory.newFunc(networkAnalyzerFactory.config, a.componentsFactory.logger, []consumer.Consumer{k8sMetadataProcessor, k8sMetadataProcessorForNodeMetric})
	// 2. Analyzer which supports gRPC protocol based on uProbe method
	uprobeAnalyzerFactory := a.componentsFactory.Analyzers[uprobeanalyzer.UprobeType.String()]
	uprobeAnalyzer := uprobeAnalyzerFactory.newFunc(uprobeAnalyzerFactory.config, a.componentsFactory.logger, []consumer.Consumer{k8sMetadataProcessor, k8sMetadataProcessorForNodeMetric})
	// 3. Layer 4 TCP events analyzer
	k8sMetadataProcessor2 := k8sProcessorFactory.newFunc(k8sProcessorFactory.config, a.componentsFactory.logger, otelExporter)
	tcpAnalyzerFactory := a.componentsFactory.Analyzers[tcpmetricanalyzer.TcpMetric.String()]
	tcpAnalyzer := tcpAnalyzerFactory.newFunc(tcpAnalyzerFactory.config, a.componentsFactory.logger, []consumer.Consumer{k8sMetadataProcessor2})
	// Initialize receiver packaged with multiple analyzers
	analyzerManager, err := analyzer.NewManager(networkAnalyzer, tcpAnalyzer, uprobeAnalyzer)
	if err != nil {
		return fmt.Errorf("error happened while creating analyzer manager: %w", err)
	}
	a.analyzerManager = analyzerManager
	udsReceiverFactory := a.componentsFactory.Receivers[udsreceiver.Uds]
	udsReceiver := udsReceiverFactory.newFunc(udsReceiverFactory.config, a.componentsFactory.logger, analyzerManager)
	a.receiver = udsReceiver
	return nil
}
