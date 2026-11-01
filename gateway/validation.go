package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func decodeData(body io.Reader) (map[string]any, error) {
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(body, 131073))
	if err != nil || len(raw) > 131072 {
		return nil, errors.New("request exceeds the 128 KiB envelope limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.Data == nil {
		return nil, errors.New(`request body must be {"data": {...}}`)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("request must contain one JSON object")
	}
	return envelope.Data, nil
}

func validateData(schema collectionSchema, data map[string]any) error {
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > 65536 {
		return errors.New("record must be valid JSON within 64 KiB")
	}
	for _, key := range schema.Required {
		if _, ok := data[key]; !ok {
			return fmt.Errorf("missing required field: %s", key)
		}
	}
	for key, value := range data {
		spec, ok := schema.Properties[key]
		if !ok {
			return fmt.Errorf("unexpected field: %s", key)
		}
		valid := false
		switch spec.Type {
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "object":
			_, valid = value.(map[string]any)
		case "array":
			_, valid = value.([]any)
		case "null":
			valid = value == nil
		case "number", "integer":
			n, ok := value.(json.Number)
			if ok {
				if spec.Type == "integer" {
					_, err = n.Int64()
					valid = err == nil && !strings.ContainsAny(n.String(), ".eE")
				} else {
					_, err = n.Float64()
					valid = err == nil
				}
			}
		}
		if !valid {
			return fmt.Errorf("field %s must be %s", key, spec.Type)
		}
	}
	return nil
}
