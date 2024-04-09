package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"os"
	"os/signal"

	"github.com/dennwc/photo-frame/protocol"
)

var (
	fMonitor = flag.Int("monitor", -1, "monitor index")
	fFull    = flag.Bool("full", true, "full screen")
	fWidth   = flag.Int("width", 1280, "window width")
	fHeight  = flag.Int("height", 800, "window height")
	fCols    = flag.Int("cols", 4, "grid columns")
	fRows    = flag.Int("rows", 2, "grid rows")
	fHost    = flag.String("host", "0.0.0.0:8181", "host to listen on")
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	flag.Parse()
	if err := run(ctx, Config{
		Host:       *fHost,
		Monitor:    *fMonitor,
		FullScreen: *fFull,
		WindowSize: image.Pt(*fWidth, *fHeight),
		GridSize:   image.Pt(*fCols, *fRows),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c Config) error {
	return New(c).Run(ctx)
}

type server struct {
	closed <-chan struct{}
	grid   chan<- image.Point
	images chan<- *image.NRGBA
}

func (s *server) addImages(ctx context.Context, arr []*protocol.Image) error {
	for _, data := range arr {
		img, err := data.Decode()
		if err != nil {
			return err
		}
		rgba, ok := img.(*image.NRGBA)
		if !ok {
			rect := img.Bounds()
			rgba = image.NewNRGBA(rect)
			draw.Draw(rgba, rect, img, image.Point{}, draw.Src)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.closed:
			return errors.New("server is closed")
		case s.images <- rgba:
		}
	}
	return nil
}

func (s *server) SetGrid(ctx context.Context, req *protocol.SetGridReq) (*protocol.SetGridResp, error) {
	if req.Cols != 0 && req.Rows != 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.closed:
			return nil, errors.New("server is closed")
		case s.grid <- image.Point{X: int(req.Cols), Y: int(req.Rows)}:
		}
	}
	if err := s.addImages(ctx, req.Images); err != nil {
		return nil, err
	}
	return &protocol.SetGridResp{}, nil
}

func (s *server) AddImage(ctx context.Context, req *protocol.AddImageReq) (*protocol.AddImageResp, error) {
	if err := s.addImages(ctx, req.Images); err != nil {
		return nil, err
	}
	return &protocol.AddImageResp{}, nil
}

//go:embed shaders/image.frag
var fragShader string

//go:embed shaders/image.vert
var vertShader string
