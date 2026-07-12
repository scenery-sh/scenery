package main

import appcfg "scenery.sh/internal/app"

func discoverConfiguredApp(appRootOption string) (string, appcfg.Config, error) {
	start, err := resolveAppRoot(appRootOption)
	if err != nil {
		return "", appcfg.Config{}, err
	}
	return appcfg.DiscoverRoot(start)
}
