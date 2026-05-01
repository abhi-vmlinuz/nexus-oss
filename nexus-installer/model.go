package main

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
)

type Page int

const (
	PageWelcome Page = iota
	PageMode
	PageRedis
	PageRegistry
	PageCredentials
	PagePorts
	PageSummary
	PageInstalling
	PageComplete
)

type Model struct {
	CurrentPage Page
	Quitting    bool

	// Configuration state
	Mode            string // dev/prod
	RedisBackend    string // docker/host
	RegistryType    string // local/dockerhub/ghcr/ecr/custom
	RegistryURL     string
	RegistryUser    string
	RegistryPass    string
	EnginePort      string
	AgentPort       string
	RegistryPort    string
	K8sNamespace    string
	RedisURL        string

	// UI components
	Cursor      int
	Inputs      []textinput.Model
	Focused     int
	Spinner     spinner.Model
	Progress    float64
	CurrentTask string

	// Installation status
	Installing   bool
	InstallError error
	Logs         []string
}

func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = StyleBrand

	return Model{
		CurrentPage:  PageWelcome,
		Mode:         "dev",
		RedisBackend: "docker",
		RegistryType: "local",
		EnginePort:   "8081",
		AgentPort:    "50051",
		RegistryPort: "5000",
		K8sNamespace: "nexus-challenges",
		RedisURL:     "redis://127.0.0.1:6379",
		Spinner:      s,
	}
}
