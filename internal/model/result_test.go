package model

import (
	"errors"
	"testing"
)

func TestOCRProviderValid(t *testing.T) {
	tests := []struct {
		provider OCRProvider
		want     bool
	}{
		{ProviderTesseract, true},
		{ProviderTextract, true},
		{OCRProvider("Tesseract"), false},
		{OCRProvider("rekognition"), false},
		{OCRProvider(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			if got := tt.provider.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func validDocumentResult() DocumentResult {
	return DocumentResult{
		JobID:      testJobID,
		Provider:   ProviderTesseract,
		Confidence: 0.92,
		Pages:      1,
		Text:       "INVOICE\nTotal 1.250.000 VND",
		Fields: []Field{
			{Name: "total", Value: "1250000", Confidence: 0.88},
			{Name: "currency", Value: "VND", Confidence: 0.99},
		},
	}
}

func TestDocumentResultValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*DocumentResult)
		wantErr error
	}{
		{name: "complete", mutate: func(*DocumentResult) {}},
		{name: "no fields extracted", mutate: func(r *DocumentResult) { r.Fields = nil }},
		{name: "textract fallback", mutate: func(r *DocumentResult) { r.Provider = ProviderTextract }},
		{name: "zero confidence", mutate: func(r *DocumentResult) { r.Confidence = 0 }},
		{name: "full confidence", mutate: func(r *DocumentResult) { r.Confidence = 1 }},

		{name: "bad job id", mutate: func(r *DocumentResult) { r.JobID = "job-1" }, wantErr: ErrInvalidJobID},
		{name: "unknown provider", mutate: func(r *DocumentResult) { r.Provider = OCRProvider("ocrspace") }, wantErr: ErrInvalidProvider},
		{name: "empty provider", mutate: func(r *DocumentResult) { r.Provider = "" }, wantErr: ErrInvalidProvider},
		{name: "confidence above one", mutate: func(r *DocumentResult) { r.Confidence = 92 }, wantErr: ErrInvalidConfidence},
		{name: "negative confidence", mutate: func(r *DocumentResult) { r.Confidence = -0.1 }, wantErr: ErrInvalidConfidence},
		{
			name:    "field confidence as a percentage",
			mutate:  func(r *DocumentResult) { r.Fields[0].Confidence = 88 },
			wantErr: ErrInvalidConfidence,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validDocumentResult()
			tt.mutate(&r)
			if err := r.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDocumentResultValidateRejectsBadFieldsAndPages(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DocumentResult)
	}{
		{name: "zero pages", mutate: func(r *DocumentResult) { r.Pages = 0 }},
		{name: "negative pages", mutate: func(r *DocumentResult) { r.Pages = -2 }},
		{name: "unnamed field", mutate: func(r *DocumentResult) { r.Fields[0].Name = "" }},
		{
			name:   "duplicate field",
			mutate: func(r *DocumentResult) { r.Fields = append(r.Fields, Field{Name: "total", Value: "99"}) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validDocumentResult()
			tt.mutate(&r)
			if err := r.Validate(); err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

func TestDocumentResultField(t *testing.T) {
	r := validDocumentResult()

	tests := []struct {
		name      string
		field     string
		wantValue string
		wantFound bool
	}{
		{name: "present", field: "total", wantValue: "1250000", wantFound: true},
		{name: "second field", field: "currency", wantValue: "VND", wantFound: true},
		{name: "absent", field: "issuer", wantFound: false},
		{name: "case sensitive", field: "Total", wantFound: false},
		{name: "empty name", field: "", wantFound: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := r.Field(tt.field)
			if found != tt.wantFound {
				t.Fatalf("Field(%q) found = %v, want %v", tt.field, found, tt.wantFound)
			}
			if got.Value != tt.wantValue {
				t.Errorf("Field(%q) value = %q, want %q", tt.field, got.Value, tt.wantValue)
			}
		})
	}
}
