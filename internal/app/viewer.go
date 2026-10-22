package app

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/gb"
)

// viewer is a screenshot the user opened to look at closely: which of the
// submission's pictures it is, and how far in and how far along it is drawn.
type viewer struct {
	// open reports whether a picture is up.
	open bool
	// index is which of the submission's pictures it is.
	index int
	// zoom is how far in it is drawn, 1 being the whole of it in the room it
	// is given.
	zoom float32
	// scroll is where the zoomed picture is scrolled to.
	scroll ui.ScrollState
	// room is the room the picture is drawn in, which the view sets as it lays
	// it out and zooming reads to keep the middle of the picture in the middle
	// of the room.
	room box
}

const (
	// zoomWhole is the picture shown whole, which is where the viewer opens.
	zoomWhole = 1
	// zoomMost is as far in as the viewer goes. Past this a screenshot of a
	// game is a field of pixels.
	zoomMost = 8
	// zoomStep is how much one click of the buttons zooms.
	zoomStep = 1.5
	// zoomPerScroll is how much one DIP of scrolling zooms, which is small
	// enough that the wheel feels continuous.
	zoomPerScroll = 0.004

	// viewerMargin is how much of the window the viewer leaves around itself.
	// It is more than the dialog it is shown in keeps from the window's edges
	// and pads itself with, so that what the viewer asks for is inside what the
	// dialog has to give it.
	viewerMargin = 96
	// viewerHeader is the room the viewer's controls take above the picture.
	viewerHeader = 44
	viewerGap    = 12
)

// openPicture shows the submission's indexth picture, whole.
func (s *store) openPicture(index int) {
	s.viewer.open = true
	s.viewer.index = index
	s.viewer.zoom = zoomWhole
	s.viewer.scroll.X, s.viewer.scroll.Y = 0, 0
	s.app.invalidate()
}

// closePicture puts the picture away.
func (s *store) closePicture() {
	s.viewer.open = false
	s.viewer.index = 0
	s.viewer.zoom = zoomWhole
	s.viewer.scroll.X, s.viewer.scroll.Y = 0, 0
	s.app.invalidate()
}

// stepPicture moves d pictures along the submission's own, wrapping: a row of
// pictures is read through, and the end of it is not a wall.
func (s *store) stepPicture(d int) {
	n := len(s.shown().profile.Images)
	if n == 0 {
		return
	}
	s.viewer.index = ((s.viewer.index+d)%n + n) % n
	s.viewer.zoom = zoomWhole
	s.viewer.scroll.X, s.viewer.scroll.Y = 0, 0
	s.app.invalidate()
}

// zoomBy draws the picture factor times as large, as far in as the viewer goes
// and no further.
func (s *store) zoomBy(factor float32) {
	before := s.viewer.zoom
	after := min(max(before*factor, zoomWhole), zoomMost)
	if after == before {
		return
	}
	s.viewer.zoom = after
	// The picture grows about its middle rather than about its top-left, which
	// is what a picture zoomed without moving the view would do.
	half := float32(s.viewer.room.W) / 2
	s.viewer.scroll.X = (s.viewer.scroll.X+half)*(after/before) - half
	half = float32(s.viewer.room.H) / 2
	s.viewer.scroll.Y = (s.viewer.scroll.Y+half)*(after/before) - half
	s.app.invalidate()
}

// viewerPanel is the picture the user opened, over the page it was opened from:
// the whole of it in the room it is given, and as far in as they zoom, which is
// what the row of small pictures has no room to show.
func (s *store) viewerPanel(c *ui.Context, images []gb.Image) {
	t := c.Theme()
	w, h := c.Size()

	// The row of pictures is read through with the arrows, and put away with
	// Escape, which the viewer's own dialog handles.
	c.OnShortcut(0, ui.KeyLeft, func() { s.stepPicture(-1) })
	c.OnShortcut(0, ui.KeyRight, func() { s.stepPicture(1) })

	ui.Column(c).
		Width(w - viewerMargin).Height(h - viewerMargin).
		Gap(viewerGap).
		Children(func() {
			ui.Row(c).FillWidth().Height(viewerHeader).
				Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("第 %d / 共 %d 张", s.viewer.index+1, len(images))).
					TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "缩小").Disabled(s.viewer.zoom <= zoomWhole).
						OnClick(func() { s.zoomBy(1 / zoomStep) })
					ui.Button(c, "放大").Disabled(s.viewer.zoom >= zoomMost).
						OnClick(func() { s.zoomBy(zoomStep) })
					ui.Button(c, "上一张").Disabled(len(images) < 2).
						OnClick(func() { s.stepPicture(-1) })
					ui.Button(c, "下一张").Disabled(len(images) < 2).
						OnClick(func() { s.stepPicture(1) })
					ui.Button(c, "关闭").OnClick(s.closePicture)
				})
			})
			s.viewerPicture(c, images[s.viewer.index])
		})
}

// viewerPicture is the picture itself, with the zooming and the moving that the
// row of small pictures has no room for.
//
// The picture is drawn in a room of its own, zoom times the one it is shown in,
// so that a zoomed one is read along by scrolling it: the room it is drawn in
// is what a picture is contained in, and the container it is scrolled by keeps
// it within its own edges without any arithmetic here.
func (s *store) viewerPicture(c *ui.Context, image gb.Image) {
	t := c.Theme()
	// What the dialog leaves of the window once the viewer's own controls have
	// taken theirs, which is the room the picture is shown whole in.
	w, h := c.Size()
	s.viewer.room = box{
		W: int(w - viewerMargin),
		H: int(h - viewerMargin - viewerHeader - viewerGap),
	}
	room := s.viewer.room
	// The picture as it was published rather than the small one the row shows:
	// what the viewer is for is seeing what the small one cannot.
	bitmap := s.app.thumbnails.get(image.URL, box{}, s.app.invalidate)

	// Named, because a picture has no text of its own for assistive technology
	// to read.
	frame := ui.Row(c).Key("viewer picture").Label("大图").
		FillWidth().Grow(1).
		Background(t.Surface).Radius(8).Clip().
		HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll || ev.DY == 0 {
				return false
			}
			// The wheel is the picture's and not the room's: over a picture
			// there is nothing to scroll to, and scrolling it is how it is
			// zoomed.
			s.zoomBy(float32(math.Exp(-float64(ev.DY) * zoomPerScroll)))
			return true
		})

	frame.Children(func() {
		if bitmap == nil {
			// A screenshot of a game is megabytes and does not arrive at once,
			// so the room is held and the wait is shown in it.
			ui.Row(c).Fill().Justify(ui.Center).AlignItems(ui.Center).
				Label("正在读取大图").
				Children(func() {
					ui.Spinner(c)
				})
			return
		}
		ui.ScrollBoth(c).Fill().TrackScroll(&s.viewer.scroll).Children(func() {
			ui.Row(c).
				Width(float32(room.W) * s.viewer.zoom).
				Height(float32(room.H) * s.viewer.zoom).
				Children(func() {
					ui.Image(c, bitmap).Fit(ui.Contain).Fill().Label("整张截图")
				})
		})
	})

	// A press on the picture moves it, which is how a zoomed one is read along.
	if dx, dy, held := frame.Dragged(); held {
		s.viewer.scroll.X -= dx
		s.viewer.scroll.Y -= dy
	}
}
