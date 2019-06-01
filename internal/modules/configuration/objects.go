package configuration

import "github.com/heptio/developer-dash/internal/describer"

var (
	pluginDescriber = &PluginListDescriber{}

	rootDescriber = describer.NewSectionDescriber(
		"/",
		"Configuration",
		pluginDescriber,
	)
)
