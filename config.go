package contracttrace

import (
	"encoding/json"
	"fmt"
	"io"
)

func ReadConfig(reader io.Reader) (Config, error) {
	config := DefaultConfig()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	target := &config
	if err := decoder.Decode(&target); err != nil {
		return Config{}, err
	}
	if target == nil {
		return Config{}, fmt.Errorf("configuration must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("configuration must contain one JSON object")
		}
		return Config{}, err
	}
	config = normalizeConfig(config)
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}
