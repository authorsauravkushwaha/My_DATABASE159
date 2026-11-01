package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	maxSingleEnvelopeBytes = 786_432    // 768 KiB single-record HTTP envelope
	maxRecordJSONBytes     = 524_288    // 512 KiB max validated record JSON
	maxBatchEnvelopeBytes  = 16_777_216 // 16 MiB batch HTTP envelope
	maxBatchItems          = 5000       // up to 5,000 mutations per atomic batch
)

type batchRecordInput struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

type batchEnvelope struct {
	Records []batchRecordInput `json:"records"`
	Deletes []string           `json:"deletes"`
}

func decodeData(body io.Reader) (map[string]any, error) {
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxSingleEnvelopeBytes+1))
	if err != nil || len(raw) > maxSingleEnvelopeBytes {
		return nil, errors.New("request exceeds the 768 KiB envelope limit")
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

func decodeBatch(body io.Reader) (batchEnvelope, error) {
	var env batchEnvelope
	raw, err := io.ReadAll(io.LimitReader(body, maxBatchEnvelopeBytes+1))
	if err != nil || len(raw) > maxBatchEnvelopeBytes {
		return env, errors.New("batch request exceeds the 16 MiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&env); err != nil {
		return env, errors.New(`batch body must be {"records": [{"id": "...", "data": {...}}], "deletes": [...]}`)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return env, errors.New("request must contain one JSON object")
	}
	total := len(env.Records) + len(env.Deletes)
	if total == 0 || total > maxBatchItems {
		return env, fmt.Errorf("batch must contain between 1 and %d operations", maxBatchItems)
	}
	return env, nil
}

func validateData(schema collectionSchema, data map[string]any) error {
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > maxRecordJSONBytes {
		return errors.New("record must be valid JSON within 512 KiB")
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
