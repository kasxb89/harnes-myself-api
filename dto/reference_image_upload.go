package dto

const (
	ReferenceImageUploadModel            = "yunshu-internal-gpt-image-2"
	ReferenceImageUploadPlugin           = "toapis-image"
	ContextKeyReferenceImageUpload       = "reference_image_upload"
	MaxReferenceImageUploadBytes   int64 = 10 << 20
)

// ReferenceImageUploadRequest is request-scoped validated image data. It must
// never be persisted in a task, asset, log, or reusable body storage.
type ReferenceImageUploadRequest struct {
	Model     string
	Image     []byte
	MIMEType  string
	Extension string
}

type ReferenceImageUploadData struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

type ReferenceImageUploadError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type ReferenceImageUploadResponse struct {
	Success bool                       `json:"success"`
	Data    *ReferenceImageUploadData  `json:"data,omitempty"`
	Error   *ReferenceImageUploadError `json:"error,omitempty"`
}
