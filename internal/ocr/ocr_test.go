package ocr

import (
	"context"
	"errors"
	"testing"
)

func TestProviderValid(t *testing.T) {
	tests := []struct {
		provider Provider
		want     bool
	}{
		{ProviderTesseract, true},
		{ProviderTextract, true},
		{Provider("Tesseract"), false},
		{Provider("rekognition"), false},
		{Provider(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			if got := tt.provider.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    Provider
		wantErr bool
	}{
		{name: "unset defaults to the free engine", value: "", want: ProviderTesseract},
		{name: "tesseract", value: "tesseract", want: ProviderTesseract},
		{name: "textract", value: "textract", want: ProviderTextract},
		{name: "case and padding", value: "  TEXTRACT ", want: ProviderTextract},
		{name: "unknown", value: "ocrspace", want: ProviderTesseract, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OCR_PROVIDER", tt.value)

			got, err := ProviderFromEnv()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ProviderFromEnv() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrUnknownProvider) {
				t.Errorf("error = %v, want ErrUnknownProvider", err)
			}
			// Even on an unreadable value the caller gets the free engine
			// rather than nothing, so a typo cannot start billing.
			if got != tt.want {
				t.Errorf("ProviderFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFake(t *testing.T) {
	doc := Document{Path: "/tmp/a.png", ContentType: "image/png"}

	t.Run("returns what it was given", func(t *testing.T) {
		fake := &Fake{Result: Result{Text: "hello", Confidence: 0.8, Pages: 2}}

		got, err := fake.Recognize(context.Background(), doc)
		if err != nil {
			t.Fatalf("Recognize() error = %v", err)
		}
		if got.Text != "hello" || got.Pages != 2 {
			t.Errorf("Result = %+v, want the canned one", got)
		}
		// A result with no provider would fail model.DocumentResult's
		// validation, so the fake fills in the default.
		if got.Provider != ProviderTesseract {
			t.Errorf("Provider = %q, want tesseract", got.Provider)
		}
		if len(fake.Calls) != 1 || fake.Calls[0] != doc {
			t.Errorf("Calls = %v, want the one document", fake.Calls)
		}
	})

	t.Run("returns the error it was given", func(t *testing.T) {
		sentinel := errors.New("engine exploded")
		fake := &Fake{Err: sentinel}

		if _, err := fake.Recognize(context.Background(), doc); !errors.Is(err, sentinel) {
			t.Errorf("Recognize() error = %v, want %v", err, sentinel)
		}
		if len(fake.Calls) != 1 {
			t.Error("a failing fake still has to record the call")
		}
	})
}

func TestProviderString(t *testing.T) {
	if got := ProviderTesseract.String(); got != "tesseract" {
		t.Errorf("String() = %q, want tesseract", got)
	}
	if got := ProviderTextract.String(); got != "textract" {
		t.Errorf("String() = %q, want textract", got)
	}
}
