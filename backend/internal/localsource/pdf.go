package localsource

import (
	"io"
	"os"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func pdfPageNames(pdfPath string) ([]string, error) {
	n, err := api.PageCountFile(pdfPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, n)
	for i := range names {
		names[i] = strconv.Itoa(i + 1)
	}
	return names, nil
}

func extractPdfPageImage(pdfPath, pageName string) (data []byte, ext string, err error) {
	f, err := os.Open(pdfPath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	var best model.Image
	err = api.ExtractImages(f, []string{pageName}, func(img model.Image, _ bool, _ int) error {
		if data != nil && img.Size <= best.Size {
			return nil
		}
		b, rerr := io.ReadAll(img)
		if rerr != nil {
			return rerr
		}
		best = img
		data = b
		return nil
	}, nil)
	if err != nil {
		return nil, "", err
	}
	if data == nil {
		return nil, "", os.ErrNotExist
	}
	switch best.FileType {
	case "png":
		ext = ".png"
	case "webp":
		ext = ".webp"
	default:
		ext = ".jpg"
	}
	return data, ext, nil
}
