package model

import (
	"errors"
	"testing"
)

func TestNormalizeContentType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "image/jpeg", want: "image/jpeg"},
		{in: "IMAGE/JPEG", want: "image/jpeg"},
		{in: "  image/jpeg  ", want: "image/jpeg"},
		{in: "image/jpeg; charset=binary", want: "image/jpeg"},
		{in: "application/pdf;", want: "application/pdf"},
		{in: "", want: ""},
		{in: ";", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeContentType(tt.in); got != tt.want {
				t.Errorf("NormalizeContentType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateContentType(t *testing.T) {
	tests := []struct {
		name        string
		docType     DocType
		contentType string
		wantErr     error
	}{
		{name: "pdf invoice", docType: DocInvoice, contentType: "application/pdf"},
		{name: "scanned invoice", docType: DocInvoice, contentType: "image/tiff"},
		{name: "id photo", docType: DocID, contentType: "image/jpeg"},
		{name: "plain image", docType: DocImage, contentType: "image/png"},
		{name: "parameters ignored", docType: DocID, contentType: "image/png; charset=binary"},
		{name: "case ignored", docType: DocID, contentType: "Image/PNG"},

		{name: "pdf is not an id document", docType: DocID, contentType: "application/pdf", wantErr: ErrUnsupportedContentType},
		{name: "zip", docType: DocInvoice, contentType: "application/zip", wantErr: ErrUnsupportedContentType},
		{name: "empty", docType: DocInvoice, contentType: "", wantErr: ErrUnsupportedContentType},
		{name: "unknown doc type", docType: DocType("receipt"), contentType: "image/png", wantErr: ErrInvalidDocType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.docType.ValidateContentType(tt.contentType)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateContentType() = %v, want %v", err, tt.wantErr)
			}
			if got := tt.docType.AcceptsContentType(tt.contentType); got != (tt.wantErr == nil) {
				t.Errorf("AcceptsContentType() = %v, want %v", got, tt.wantErr == nil)
			}
		})
	}
}

func TestContentTypesIsACopy(t *testing.T) {
	got := DocID.ContentTypes()
	if len(got) == 0 {
		t.Fatal("DocID accepts nothing")
	}
	got[0] = "application/zip"
	if DocID.AcceptsContentType("application/zip") {
		t.Error("mutating the returned slice changed what DocID accepts")
	}
}

func TestEveryDocTypeAcceptsSomething(t *testing.T) {
	for _, docType := range []DocType{DocInvoice, DocID, DocImage} {
		if len(docType.ContentTypes()) == 0 {
			t.Errorf("%s accepts no content type, so no upload of it can ever be created", docType)
		}
	}
	if got := DocType("receipt").ContentTypes(); len(got) != 0 {
		t.Errorf("an unknown doc type accepts %v, want nothing", got)
	}
}
