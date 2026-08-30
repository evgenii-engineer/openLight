package telegram

import "testing"

func TestIncomingMessageSurfacesNonImageDocuments(t *testing.T) {
	t.Parallel()

	upd := update{
		UpdateID: 1,
		Message: &tgMessage{
			MessageID: 9,
			Caption:   "quarterly numbers",
			Chat:      tgChat{ID: 100},
			From:      tgUser{ID: 200},
			Document: &tgFile{
				FileID:   "doc-1",
				FileName: "report.pdf",
				MimeType: "application/pdf",
				FileSize: 4096,
			},
		},
	}

	msg, ok := upd.incomingMessage()
	if !ok {
		t.Fatal("a PDF upload must produce an incoming message; it used to be dropped entirely")
	}
	if msg.Document == nil {
		t.Fatal("expected a Document attachment")
	}
	if msg.Document.FileID != "doc-1" || msg.Document.FileName != "report.pdf" {
		t.Fatalf("document metadata wrong: %+v", msg.Document)
	}
	if msg.Document.MimeType != "application/pdf" || msg.Document.FileSize != 4096 {
		t.Fatalf("document mime/size wrong: %+v", msg.Document)
	}
	if msg.Document.Caption != "quarterly numbers" || msg.Text != "quarterly numbers" {
		t.Fatalf("caption not surfaced: %+v / %q", msg.Document, msg.Text)
	}
	if msg.Source != "telegram_document" {
		t.Fatalf("source = %q, want telegram_document", msg.Source)
	}
	if msg.Image != nil {
		t.Fatal("a PDF must not also be routed to the image inbox")
	}
}

func TestImageDocumentsStayOnTheImagePath(t *testing.T) {
	t.Parallel()

	upd := update{
		UpdateID: 1,
		Message: &tgMessage{
			MessageID: 9,
			Chat:      tgChat{ID: 100},
			From:      tgUser{ID: 200},
			Document: &tgFile{
				FileID:   "img-1",
				FileName: "screenshot.png",
				MimeType: "image/png",
			},
		},
	}

	msg, ok := upd.incomingMessage()
	if !ok {
		t.Fatal("expected an incoming message")
	}
	// An image sent as a file must reach vision/OCR exactly as before —
	// routing it to both inboxes would analyse it twice.
	if msg.Image == nil {
		t.Fatalf("image document lost its Image attachment: %+v", msg)
	}
	if msg.Document != nil {
		t.Fatal("image documents must not also surface as generic documents")
	}
	if msg.Source != "telegram_image" {
		t.Fatalf("source = %q, want telegram_image", msg.Source)
	}
}

func TestDocumentWithoutAFileIDIsIgnored(t *testing.T) {
	t.Parallel()

	upd := update{
		UpdateID: 1,
		Message: &tgMessage{
			MessageID: 9,
			Chat:      tgChat{ID: 100},
			From:      tgUser{ID: 200},
			Document:  &tgFile{FileName: "empty.bin"},
		},
	}

	if msg, ok := upd.incomingMessage(); ok && msg.Document != nil {
		t.Fatalf("a document with no file id should not be surfaced: %+v", msg.Document)
	}
}

func TestPlainTextIsUnaffectedByTheDocumentPath(t *testing.T) {
	t.Parallel()

	upd := update{
		UpdateID: 1,
		Message: &tgMessage{
			MessageID: 9,
			Chat:      tgChat{ID: 100},
			From:      tgUser{ID: 200},
			Text:      "/status",
		},
	}

	msg, ok := upd.incomingMessage()
	if !ok || msg.Document != nil || msg.Text != "/status" || msg.Source != "telegram" {
		t.Fatalf("plain text routing changed: ok=%v msg=%+v", ok, msg)
	}
}
