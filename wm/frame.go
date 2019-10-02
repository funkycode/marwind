package wm

import (
	"fmt"
	"github.com/BurntSushi/xgb/xfixes"

	"github.com/BurntSushi/xgb/xproto"
	"github.com/patrislav/marwind/x11"
)

type winType uint8

const (
	winTypeUnknown winType = iota
	winTypeNormal
	winTypeDock
)

type frame struct {
	col      *column
	parent   xproto.Window
	client   *client
	height   uint32
	typ      winType
	mapped   bool
	geom     x11.Geom
	titlebar *titlebar
}

func (wm *WM) createFrame(win xproto.Window, typ winType) (*frame, error) {
	f := &frame{typ: typ}
	c := &client{window: win, frame: f}
	f.client = c

	f.setInitialProperties()

	if typ == winTypeNormal {
		parent, err := wm.createParent()
		if err != nil {
			return nil, fmt.Errorf("failed to create parent: %v", err)
		}

		if err := f.reparent(parent); err != nil {
			return nil, err
		}

		f.titlebar = newTitlebar(f, wm.titlebarConfig)
	}
	return f, nil
}

func (f *frame) reparent(parent xproto.Window) error {
	if err := xproto.ReparentWindowChecked(x11.X, f.client.window, parent, 0, 0).Check(); err != nil {
		return fmt.Errorf("could not reparent window: %s", err)
	}
	f.parent = parent
	xproto.ChangeSaveSet(x11.X, xfixes.SaveSetModeInsert, f.client.window)
	return nil
}

// doMap causes both the client window and the frame to be mapped
func (f *frame) doMap() error {
	if f.parent != 0 {
		if err := xproto.MapWindowChecked(x11.X, f.parent).Check(); err != nil {
			return fmt.Errorf("could not map parent: %v", err)
		}
	}
	if err := xproto.MapWindowChecked(x11.X, f.client.window).Check(); err != nil {
		return fmt.Errorf("could not map window: %v", err)
	}
	f.mapped = true
	return nil
}

// doUnmap causes the client window to be unmapped. This in turn sends the UnmapNotify event
// that is then handled by (*frame).onUnmap
func (f *frame) doUnmap() error {
	if err := xproto.UnmapWindowChecked(x11.X, f.client.window).Check(); err != nil {
		return fmt.Errorf("could not unmap window: %v", err)
	}
	return nil
}

// onUnmap is called when the WM receives the UnmapNotify event (e.g. when the client window
// is closed by user action or when requested by the program itself
func (f *frame) onUnmap() error {
	if !f.mapped {
		return nil
	}
	if f.parent != 0 {
		if err := xproto.UnmapWindowChecked(x11.X, f.parent).Check(); err != nil {
			return fmt.Errorf("could not unmap parent: %v", err)
		}
	}
	f.mapped = false
	return nil
}

// onDestroy is called when the WM receives the DestroyNotify event
func (f *frame) onDestroy() error {
	if f.parent != 0 {
		if err := xproto.DestroyWindowChecked(x11.X, f.parent).Check(); err != nil {
			return fmt.Errorf("could not destroy parent: %v", err)
		}
	}
	return nil
}

func (f *frame) onProperty(atom xproto.Atom) {
	switch atom {
	case x11.Atom("_NET_WM_NAME"):
		f.setTitleProperty()
	}
}

func (f *frame) setInitialProperties() {
	f.setTitleProperty()
}

func (f *frame) setTitleProperty() {
	if v, err := x11.GetWindowTitle(f.client.window); err == nil {
		f.client.title = v
		if f.titlebar != nil {
			f.titlebar.draw()
		}
	}
}

func (f *frame) workspace() *workspace {
	if f.col != nil {
		return f.col.ws
	}
	return nil
}

func (wm *WM) getFrameDecorations(f *frame) x11.Dimensions {
	if f.parent == 0 {
		return x11.Dimensions{0, 0, 0, 0}
	}
	var bar uint32
	border := uint32(wm.config.BorderWidth)
	if wm.config.TitleBarHeight > 0 {
		bar = uint32(wm.config.TitleBarHeight) + 1
	}
	return x11.Dimensions{
		Top:    border + bar,
		Right:  border,
		Bottom: border,
		Left:   border,
	}
}

// createParent generates an X window and sets it up so that it can be used for reparenting
func (wm *WM) createParent() (xproto.Window, error) {
	id, err := xproto.NewWindowId(x11.X)
	if err != nil {
		return 0, err
	}
	visual := x11.Screen.RootVisual
	vdepth := x11.Screen.RootDepth
	err = xproto.CreateWindowChecked(x11.X, vdepth, id, x11.Screen.Root,
		0, 0, 1, 1, 0, xproto.WindowClassInputOutput, visual,
		xproto.CwBackPixel|xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{
			wm.config.BorderColor,
			1,
			xproto.EventMaskSubstructureRedirect |
				xproto.EventMaskExposure |
				xproto.EventMaskButtonPress |
				xproto.EventMaskButtonRelease |
				xproto.EventMaskFocusChange,
		}).Check()
	if err != nil {
		return 0, fmt.Errorf("could not create window: %s", err)
	}
	return id, nil
}
