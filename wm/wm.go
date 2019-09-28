package wm

import (
	"fmt"
	"log"

	"github.com/BurntSushi/xgb/xproto"
	"github.com/patrislav/marwind/keysym"
	"github.com/patrislav/marwind/x11"
)

const maxWorkspaces = 10

// WM is a struct representing the Window Manager
type WM struct {
	outputs    []*output
	keymap     keysym.Keymap
	actions    []*action
	config     Config
	workspaces [maxWorkspaces]*workspace
	activeWin  xproto.Window
}

// New initializes a WM and creates an X11 connection
func New(config Config) (*WM, error) {
	wm := &WM{config: config}
	if err := x11.CreateConnection(); err != nil {
		return nil, fmt.Errorf("failed to create WM: %v", err)
	}
	return wm, nil
}

// Init initializes the WM
func (wm *WM) Init() error {
	if err := x11.InitConnection(); err != nil {
		return fmt.Errorf("failed to init WM: %v", err)
	}
	if err := wm.becomeWM(); err != nil {
		if _, ok := err.(xproto.AccessError); ok {
			return fmt.Errorf("could not become WM, possibly another WM is already running")
		}
		return fmt.Errorf("could not become WM: %v", err)
	}
	km, err := keysym.LoadKeyMapping(x11.X)
	if err != nil {
		return fmt.Errorf("failed to load key mapping: %v", err)
	}
	wm.keymap = *km
	wm.actions = initActions(wm)
	if err := wm.grabKeys(); err != nil {
		return fmt.Errorf("failed to grab keys: %v", err)
	}

	o := newOutput(x11.Geom{
		X: 0, Y: 0,
		W: uint32(x11.Screen.WidthInPixels),
		H: uint32(x11.Screen.HeightInPixels),
	})
	for i := 0; i < maxWorkspaces; i++ {
		wm.workspaces[i] = newWorkspace(uint8(i))
	}
	if err := o.addWorkspace(wm.workspaces[0]); err != nil {
		return fmt.Errorf("failed to add workspace to output: %v", err)
	}
	wm.outputs = append(wm.outputs, o)

	if err := x11.SetWMName("Marwind"); err != nil {
		return fmt.Errorf("failed to set WM name: %v", err)
	}
	return nil
}

// Close cleans up the WM's resources
func (wm *WM) Close() {
	if x11.X != nil {
		x11.X.Close()
	}
}

// Run starts the WM's X event loop
func (wm *WM) Run() error {
	for {
		xev, err := x11.X.WaitForEvent()
		if err != nil {
			// TODO: log the error
			continue
		}
		log.Println(xev)
		switch e := xev.(type) {
		// TODO: handle all the events
		case xproto.KeyPressEvent:
			if err := wm.handleKeyPressEvent(e); err != nil {
				log.Println(err)
			}

		case xproto.EnterNotifyEvent:
			if err := wm.setFocus(e.Event, e.Time); err != nil {
				log.Println("Failed to set focus:", err)
			}

		case xproto.ConfigureRequestEvent:
			ev := xproto.ConfigureNotifyEvent{
				Event:            e.Window,
				Window:           e.Window,
				AboveSibling:     0,
				X:                e.X,
				Y:                e.Y,
				Width:            e.Width,
				Height:           e.Height,
				BorderWidth:      0,
				OverrideRedirect: false,
			}
			xproto.SendEventChecked(x11.X, false, e.Window, xproto.EventMaskStructureNotify, string(ev.Bytes()))

		case xproto.MapRequestEvent:
			if attr, err := xproto.GetWindowAttributes(x11.X, e.Window).Reply(); err != nil || !attr.OverrideRedirect {
				if err := wm.manageWindow(e.Window); err != nil {
					log.Println("Failed to manage a window:", err)
				}
			}

		case xproto.UnmapNotifyEvent:
			f := wm.findFrame(func(frm *frame) bool { return frm.client.window == e.Window })
			if f != nil {
				if err := f.onUnmap(); err != nil {
					log.Println("Failed to unmap frame's parent:", err)
					continue
				}
				switch f.typ {
				case winTypeNormal:
					if ws := f.workspace(); ws != nil {
						ws.updateTiling()
						if err := wm.renderWorkspace(ws); err != nil {
							log.Println("Failed to render workspace:", err)
						}
					}
				case winTypeDock:
					if err := wm.renderOutput(wm.outputs[0]); err != nil {
						log.Println("Failed to render output:", err)
					}
				}
			}

		case xproto.DestroyNotifyEvent:
			f := wm.findFrame(func(frm *frame) bool { return frm.client.window == e.Window })
			if f != nil {
				if err := f.onDestroy(); err != nil {
					log.Println("Failed to destroy frame's parent:", err)
					continue
				}
				if err := wm.deleteFrame(f); err != nil {
					log.Println("Failed to delete the frame:", err)
				}
			}
		}
	}
}

// becomeWM updates the X root window's attributes in an attempt to manage other windows
func (wm *WM) becomeWM() error {
	evtMask := []uint32{
		xproto.EventMaskKeyPress |
			xproto.EventMaskKeyRelease |
			xproto.EventMaskButtonPress |
			xproto.EventMaskButtonRelease |
			xproto.EventMaskPropertyChange |
			xproto.EventMaskFocusChange |
			xproto.EventMaskStructureNotify |
			xproto.EventMaskSubstructureRedirect,
	}
	return xproto.ChangeWindowAttributesChecked(x11.X, x11.Screen.Root, xproto.CwEventMask, evtMask).Check()
}

// grabKeys attempts to get a sole ownership of certain key combinations
func (wm *WM) grabKeys() error {
	for _, action := range wm.actions {
		for _, code := range action.codes {
			cookie := xproto.GrabKeyChecked(
				x11.X,
				false,
				x11.Screen.Root,
				uint16(action.modifiers),
				code,
				xproto.GrabModeAsync,
				xproto.GrabModeAsync,
			)
			if err := cookie.Check(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (wm *WM) findFrame(predicate func(*frame) bool) *frame {
	for _, ws := range wm.workspaces {
		for _, col := range ws.columns {
			for _, f := range col.frames {
				if predicate(f) {
					return f
				}
			}
		}
	}
	for _, o := range wm.outputs {
		for area := range o.dockAreas {
			for _, f := range o.dockAreas[area] {
				if predicate(f) {
					return f
				}
			}
		}
	}
	return nil
}

func (wm *WM) deleteFrame(f *frame) error {
	for _, o := range wm.outputs {
		if o.deleteFrame(f) {
			if err := wm.setFocus(x11.Screen.Root, xproto.TimeCurrentTime); err != nil {
				return err
			}
			return wm.renderOutput(o)
		}
	}
	return fmt.Errorf("could not find frame to delete: %v", f)
}

func (wm *WM) handleKeyPressEvent(e xproto.KeyPressEvent) error {
	sym := wm.keymap[e.Detail][0]
	for _, action := range wm.actions {
		if sym == action.sym && e.State == uint16(action.modifiers) {
			return action.act()
		}
	}
	return nil
}