package uihost

import (
	"errors"
	"io"

	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/ui"
)

// picker adapts the platform file chooser to ui.FilePicker. The errors are
// translated here, so the UI acts on its own classification and never imports
// the window layer.
type picker struct {
	exp *explorer.Explorer
}

func (p picker) ChooseFile() (io.ReadCloser, error) {
	rc, err := p.exp.ChooseFile()
	if err != nil {
		return nil, pickerErr(err)
	}
	return rc, nil
}

func (p picker) CreateFile(name string) (io.WriteCloser, error) {
	w, err := p.exp.CreateFile(name)
	if err != nil {
		return nil, pickerErr(err)
	}
	return w, nil
}

// pickerErr maps the platform errors onto the two outcomes the UI
// distinguishes. Any other failure keeps its text: it is the only detail the
// user can act on.
func pickerErr(err error) error {
	switch {
	case errors.Is(err, explorer.ErrNotAvailable):
		return ui.ErrPickerUnavailable
	case errors.Is(err, explorer.ErrUserDecline):
		return ui.ErrPickerDeclined
	}
	return err
}
