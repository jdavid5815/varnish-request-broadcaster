package main

import (
	"encoding/json"
	"os"

	ini "github.com/jdavid5815/ini"
)

func LoadCachesFromJson(configPath string) ([]Group, error) {

	var groups []Group

	_, err := os.Stat(configPath)
	if err != nil {
		return nil, err
	}
	fileContent, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(fileContent, &groups)
	if err != nil {
		return nil, err
	} else {
		return groups, nil
	}
}

func LoadCachesFromIni(configPath string) ([]Group, error) {

	var groups []Group

	cfg, err := ini.Load(configPath)
	if err != nil {
		return nil, err
	}
	for _, s := range cfg.Sections() {
		var g Group
		for _, k := range s.Keys() {
			var c Vcache
			c.Name = k.Name()
			c.Address = k.Value()
			g.Caches = append(g.Caches, c)

		}
		g.Name = s.Name()
		groups = append(groups, g)
	}
	return groups, nil
}
