package main

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-gl/gl/all-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"

	"github.com/dennwc/photo-frame/protocol"
)

const debug = false

type Config struct {
	Host       string
	Monitor    int
	FullScreen bool
	WindowSize image.Point
	GridSize   image.Point
}

func New(c Config) *App {
	if c.Host == "" {
		c.Host = "127.0.0.1:8181"
	}
	return &App{conf: c}
}

type coordUV struct {
	Pos [2]float32
	UV  [2]float32
}
type triangle [3]uint32

type App struct {
	conf     Config
	close    []func()
	lis      net.Listener
	gridReq  chan image.Point
	imageReq chan *image.NRGBA

	n       int
	win     *glfw.Window
	resized bool

	vertArr uint32
	vertBuf uint32
	elemBuf uint32
	prog    uint32

	textures     []uint32
	showTextures []uint32
	tmpTexture   uint32
	curIndex     int
	imageSizes   []image.Point

	triangles   [][2]triangle
	coordsAndUV [][4]coordUV
}

func (a *App) onClose(fnc func()) {
	a.close = append(a.close, fnc)
}

func (a *App) Run(ctx context.Context) error {
	a.n = a.conf.GridSize.X * a.conf.GridSize.Y
	if a.n <= 0 {
		return fmt.Errorf("invalid width or height")
	}
	defer func() {
		for i := len(a.close) - 1; i >= 0; i-- {
			a.close[i]()
		}
		a.close = nil
	}()

	if err := a.startListener(); err != nil {
		return err
	}

	runtime.LockOSThread()
	a.onClose(runtime.UnlockOSThread)

	if err := glfw.Init(); err != nil {
		return err
	}
	a.onClose(glfw.Terminate)

	if err := a.initWindow(); err != nil {
		return err
	}
	if err := a.initGL(); err != nil {
		return err
	}
	a.initVertArr()
	if err := a.compileShaders(); err != nil {
		return err
	}
	a.initTextures()
	a.initElemBuf()
	a.initVertBuf()
	a.startServer(ctx)
	return a.loop(ctx)
}

func (a *App) initWindow() error {
	glfw.DefaultWindowHints()
	glfw.WindowHint(glfw.DoubleBuffer, glfw.True)
	glfw.WindowHint(glfw.ContextVersionMajor, 3)
	glfw.WindowHint(glfw.ContextVersionMinor, 3)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)
	glfw.WindowHint(glfw.AutoIconify, glfw.False)

	var monitor *glfw.Monitor
	if a.conf.Monitor < 0 {
		monitor = glfw.GetPrimaryMonitor()
	} else {
		monitors := glfw.GetMonitors()
		if a.conf.Monitor >= len(monitors) {
			return fmt.Errorf("no monitor found")
		}
		monitor = monitors[a.conf.Monitor]
	}

	var winMonitor *glfw.Monitor
	winSize := a.conf.WindowSize
	if a.conf.FullScreen {
		winMonitor = monitor
		mode := monitor.GetVideoMode()
		winSize = image.Pt(mode.Width, mode.Height)
	}

	win, err := glfw.CreateWindow(winSize.X, winSize.Y, "Photo Frame", winMonitor, nil)
	if err != nil {
		return err
	}
	a.onClose(win.Destroy)

	win.SetFramebufferSizeCallback(func(w *glfw.Window, width int, height int) {
		a.resized = true
	})
	win.SetKeyCallback(func(w *glfw.Window, key glfw.Key, scancode int, action glfw.Action, mods glfw.ModifierKey) {
		if key == glfw.KeyEscape {
			win.SetShouldClose(true)
		}
	})
	a.win = win
	return nil
}

func (a *App) initGL() error {
	a.win.MakeContextCurrent()
	if err := gl.Init(); err != nil {
		return fmt.Errorf("OpenGL init failed: %w", err)
	}
	slog.Info("OpenGL initialized", "vers", gl.GoStr(gl.GetString(gl.VERSION)))

	if debug {
		gl.Enable(gl.DEBUG_OUTPUT)
	}
	gl.DebugMessageCallback(func(source uint32, gltype uint32, id uint32, severity uint32, length int32, message string, userParam unsafe.Pointer) {
		switch severity {
		case gl.DEBUG_SEVERITY_NOTIFICATION:
			slog.Debug(message)
		default:
			slog.Info(message, "severity", fmt.Sprintf("%x", severity))
		}
	}, nil)
	return nil
}

func (a *App) initVertArr() {
	gl.GenVertexArrays(1, &a.vertArr)
	gl.BindVertexArray(a.vertArr)
	a.onClose(func() {
		gl.DeleteVertexArrays(1, &a.vertArr)
	})
	glCheckErr()
}

func (a *App) compileShaders() error {
	vert, err := compileShader(gl.VERTEX_SHADER, vertShader)
	if err != nil {
		return fmt.Errorf("cannot compile vertex shader: %w", err)
	}
	a.onClose(func() {
		gl.DeleteShader(vert)
	})

	frag, err := compileShader(gl.FRAGMENT_SHADER, fragShader)
	if err != nil {
		return fmt.Errorf("cannot compile vertex shader: %w", err)
	}
	a.onClose(func() {
		gl.DeleteShader(frag)
	})

	prog, err := compileProgram(vert, frag)
	if err != nil {
		return err
	}
	a.onClose(func() {
		gl.DeleteProgram(prog)
	})
	gl.UseProgram(prog)
	a.prog = prog
	return nil
}

func (a *App) initTextures() {
	a.textures = make([]uint32, a.n+1)
	gl.GenTextures(int32(len(a.textures)), &a.textures[0])
	a.onClose(func() {
		gl.DeleteTextures(int32(len(a.textures)), &a.textures[0])
	})
	glCheckErr()

	a.showTextures = make([]uint32, a.n)
	copy(a.showTextures, a.textures[:a.n])
	a.tmpTexture = a.textures[len(a.textures)-1]
	a.imageSizes = make([]image.Point, a.n)
	a.curIndex = -1
}

func (a *App) initElemBuf() {
	gl.GenBuffers(1, &a.elemBuf)
	glCheckErr()
	a.triangles = make([][2]triangle, a.n)
	for i := range a.n {
		triangles := &a.triangles[i]
		t1 := &triangles[0]
		t2 := &triangles[1]

		t1[0] = uint32(4*i + 2)
		t1[1] = uint32(4*i + 0)
		t1[2] = uint32(4*i + 1)

		t2[0] = uint32(4*i + 2)
		t2[1] = uint32(4*i + 3)
		t2[2] = uint32(4*i + 1)
	}
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, a.elemBuf)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(a.triangles)*int(unsafe.Sizeof(a.triangles[0])), gl.Ptr(a.triangles), gl.STATIC_DRAW)
	glCheckErr()
}

func (a *App) initVertBuf() {
	gl.GenBuffers(1, &a.vertBuf)
	glCheckErr()
	a.coordsAndUV = make([][4]coordUV, a.n)
	a.updateCoords()
}

func (a *App) updateVertBuf() {
	gl.BindBuffer(gl.ARRAY_BUFFER, a.vertBuf)
	gl.BufferData(gl.ARRAY_BUFFER, len(a.coordsAndUV)*int(unsafe.Sizeof(a.coordsAndUV[0])), gl.Ptr(a.coordsAndUV), gl.STATIC_DRAW)
	glCheckErr()
}

func (a *App) updateCoords() {
	w, h := a.win.GetFramebufferSize()
	a.updateCoordsWith(image.Point{X: w, Y: h})
}

func (a *App) updateCoordsWith(screen image.Point) {
	defer a.updateVertBuf()

	grid := a.conf.GridSize
	maxSize := image.Point{}
	for _, sz := range a.imageSizes {
		if sz == (image.Point{}) {
			continue
		}
		if maxSize.X <= 0 || maxSize.X < sz.X {
			maxSize.X = sz.X
		}
		if maxSize.Y <= 0 || maxSize.Y < sz.Y {
			maxSize.Y = sz.Y
		}
	}
	if maxSize == (image.Point{}) {
		maxSize = image.Point{X: screen.X / grid.X, Y: screen.Y / grid.Y}
	}
	idealSize := image.Point{X: maxSize.X * grid.X, Y: maxSize.Y * grid.Y}

	screenScaleW, screenScaleH := calcScale(screen, idealSize)
	screenOffX, screenOffY := calcOffset(screenScaleW, screenScaleH, 2, 2)

	cellSize := image.Point{X: idealSize.X / grid.X, Y: idealSize.Y / grid.Y}

	gridFx := 2.0 * screenScaleW / float64(grid.X)
	gridFy := 2.0 * screenScaleH / float64(grid.Y)
	for i, sz := range a.imageSizes {
		corners := &a.coordsAndUV[i]
		yi := i / grid.X
		xi := i % grid.X
		for ci := range 4 {
			cx, cy := ci%2, ci/2
			corners[ci] = coordUV{
				Pos: [2]float32{
					float32(screenOffX + float64(xi+cx)*gridFx - 1),
					float32(screenOffY + float64(yi+cy)*gridFy - 1),
				},
				UV: [2]float32{float32(cx), float32(cy)},
			}
		}
		scaleW, scaleH := calcScale(cellSize, sz)
		dx, dy := calcOffset(scaleW, scaleH, gridFx, gridFy)

		for ci := range corners {
			c := &corners[ci]
			switch ci {
			case 0, 2:
				c.Pos[0] += float32(dx)
			case 1, 3:
				c.Pos[0] -= float32(dx)
			}
			switch ci {
			case 0, 1:
				c.Pos[1] += float32(dy)
			case 2, 3:
				c.Pos[1] -= float32(dy)
			}
		}
	}
}

func (a *App) addImage(img *image.NRGBA) {
	a.curIndex++
	a.curIndex %= a.n
	slog.Info("image received", "i", a.curIndex, "n", a.n, "w", img.Rect.Dx(), "h", img.Rect.Dy())
	a.imageSizes[a.curIndex] = img.Rect.Size()
	tmp, dst := &a.tmpTexture, &a.showTextures[a.curIndex]
	gl.BindTexture(gl.TEXTURE_2D, *tmp)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(img.Rect.Dx()), int32(img.Rect.Dy()), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
	glCheckErr()
	*dst, *tmp = *tmp, *dst
	a.updateCoords()
}

func (a *App) startListener() error {
	l, err := net.Listen("tcp", a.conf.Host)
	if err != nil {
		return err
	}
	a.lis = l
	a.onClose(func() {
		l.Close()
	})
	return nil
}

func (a *App) startServer(ctx context.Context) {
	gridReq := make(chan image.Point)
	imageReq := make(chan *image.NRGBA, a.n)
	a.gridReq = gridReq
	a.imageReq = imageReq
	done := ctx.Done()

	hsrv := &http.Server{
		Addr: a.conf.Host,
		Handler: protocol.NewPhotoFrameServer(&server{
			closed: done,
			grid:   gridReq,
			images: imageReq,
		}),
	}
	go func() {
		if err := hsrv.Serve(a.lis); err != nil {
			slog.Error("failed to serve api", "err", err)
		}
	}()
}

func (a *App) loop(ctx context.Context) error {
	done := ctx.Done()
	gl.UseProgram(a.prog)
	if debug {
		gl.ClearColor(1, 0, 1, 1)
	} else {
		gl.ClearColor(0, 0, 0, 1)
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, a.vertBuf)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, a.elemBuf)
	glSetProgAttr(0, 2, gl.FLOAT, false, unsafe.Sizeof(coordUV{}), unsafe.Offsetof(coordUV{}.Pos))
	glSetProgAttr(1, 2, gl.FLOAT, false, unsafe.Sizeof(coordUV{}), unsafe.Offsetof(coordUV{}.UV))

	width, height := a.win.GetFramebufferSize()
	gl.Viewport(0, 0, int32(width), int32(height))

	const fps = 60
	ticker := time.NewTicker(time.Second / fps)
	defer ticker.Stop()
	for {
		if a.win.ShouldClose() {
			return nil
		}
		select {
		case <-done:
			return nil
		case grid := <-a.gridReq:
			_ = grid // ignore for now
			continue
		case img := <-a.imageReq:
			a.addImage(img)
			continue
		case <-ticker.C:
		}
		if a.resized {
			a.resized = false
			width, height = a.win.GetFramebufferSize()
			gl.Viewport(0, 0, int32(width), int32(height))
			a.updateCoords()
		}
		gl.Clear(gl.COLOR_BUFFER_BIT)

		for i, tex := range a.showTextures {
			gl.BindTexture(gl.TEXTURE_2D, tex)
			gl.DrawElementsWithOffset(gl.TRIANGLES, 6, gl.UNSIGNED_INT, uintptr(i)*6*4)
		}
		glCheckErr()

		a.win.SwapBuffers()
		glfw.PollEvents()
	}
}
