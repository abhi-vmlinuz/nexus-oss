package main

import "github.com/charmbracelet/lipgloss"

var (
	ColorCyan   = lipgloss.Color("#00E5FF")
	ColorGreen  = lipgloss.Color("#69FF47")
	ColorYellow = lipgloss.Color("#FFD600")
	ColorRed    = lipgloss.Color("#FF5252")
	ColorGray   = lipgloss.Color("#546E7A")
	ColorWhite  = lipgloss.Color("#FFFFFF")

	StyleBrand = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCyan)

	StyleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorWhite).
			Background(lipgloss.Color("#263238")).
			Padding(0, 1).
			MarginBottom(1)

	StyleStep = lipgloss.NewStyle().
			Foreground(ColorGray).
			Italic(true)

	StyleGray = lipgloss.NewStyle().
			Foreground(ColorGray)

	StyleSelected = lipgloss.NewStyle().
			Foreground(ColorCyan).
			Bold(true)

	StyleUnselected = lipgloss.NewStyle().
			Foreground(ColorWhite)

	StyleInputPrompt = lipgloss.NewStyle().
				Foreground(ColorCyan).
				Bold(true)

	StyleSuccess = lipgloss.NewStyle().
			Foreground(ColorGreen).
			Bold(true)

	StyleError = lipgloss.NewStyle().
			Foreground(ColorRed).
			Bold(true)

	StyleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#263238")).
			Padding(1, 2)

	StyleFooter = lipgloss.NewStyle().
			Foreground(ColorGray).
			MarginTop(1)
)
