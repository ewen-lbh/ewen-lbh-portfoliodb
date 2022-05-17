package ortfodb

// Functions to analyze media files.
// Used to go from a ParsedDescription struct to a Work struct.

import (
	"fmt"
	"image"

	// Supported formats
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "github.com/hullerob/go.farbfeld"
	_ "github.com/jbuchbinder/gopnm" // PBM, PGM and PPM
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/ccitt"
	_ "golang.org/x/image/riff"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/vp8"
	_ "golang.org/x/image/vp8l"
	_ "golang.org/x/image/webp"

	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/lafriks/go-svg"
	"github.com/metal3d/go-slugify"
	ffmpeg "github.com/ssttevee/go-ffmpeg"
	"github.com/tcolgate/mp3"
)

// ImageDimensions represents metadata about a media as it's extracted from its file.
type ImageDimensions struct {
	Width       int
	Height      int
	AspectRatio float32
}

// Thumbnail represents a thumbnail.
type Thumbnail struct {
	Type        string
	ContentType string
	Format      string
	Source      string
	Dimensions  ImageDimensions
}

// Media represents a media object inserted in the work object's media array.
type Media struct {
	ID    string
	Alt   string
	Title string
	// Source is the media's path, verbatim from the embed declaration (what's actually written in the description file).
	Source string
	// Path is the media's path, relative to (media directory)/(work ID).
	// See Configuration.Media.At.
	Path        string
	ContentType string
	Size        uint64 // In bytes
	Dimensions  ImageDimensions
	Duration    uint // In seconds
	Online      bool // Whether the media is hosted online (referred to by an URL)
	Attributes  MediaAttributes
	HasSound    bool // The media is either an audio file or a video file that contains an audio stream
}

func (ctx *RunContext) AbsolutePathToMedia(media Media) string {
	return path.Join(ctx.Config.Media.At, ctx.CurrentWorkID, media.Path)
}

// GetImageDimensions returns an ImageDimensions object, given a pointer to a file.
func GetImageDimensions(file *os.File) (ImageDimensions, error) {
	img, _, err := image.Decode(file)
	if err != nil {
		return ImageDimensions{}, err
	}
	// Get height & width
	height := img.Bounds().Dy()
	width := img.Bounds().Dx()
	// Get aspect ratio
	ratio := float32(width) / float32(height)
	return ImageDimensions{width, height, ratio}, nil
}

// GetSVGDimensions returns an ImageDimensions object, given a pointer to a SVG file.
// If neither viewBox nor width & height attributes are set, the resulting dimensions will be 0x0.
func GetSVGDimensions(file *os.File) (ImageDimensions, error) {
	svg, err := svg.Parse(file, svg.IgnoreErrorMode)
	if err != nil {
		return ImageDimensions{}, fmt.Errorf("while parsing SVG file: %w", err)
	}

	var height, width float64
	if svg.Width != "" && svg.Height != "" {
		height, err = strconv.ParseFloat(svg.Height, 32)
		if err != nil {
			return ImageDimensions{}, fmt.Errorf("cannot parse SVG height attribute as a number: %w", err)
		}
		width, err = strconv.ParseFloat(svg.Width, 32)
		if err != nil {
			return ImageDimensions{}, fmt.Errorf("cannot parse SVG width attribute as a number: %w", err)
		}
	} else if svg.ViewBox.H != 0 && svg.ViewBox.W != 0 {
		height = float64(svg.ViewBox.H)
		width = float64(svg.ViewBox.W)
	} else {
		return ImageDimensions{}, fmt.Errorf("cannot determine dimensions of SVG file")
	}

	return ImageDimensions{
		Width:       int(width),
		Height:      int(height),
		AspectRatio: float32(width) / float32(height),
	}, nil
}

// AnalyzeMediaFile analyzes the file at its absolute filepath filename and returns a Media struct, merging the analysis' results with information from the matching MediaEmbedDeclaration.
func (ctx *RunContext) AnalyzeMediaFile(filename string, embedDeclaration MediaEmbedDeclaration) (Media, error) {
	file, err := os.Open(filename)
	defer file.Close()
	if err != nil {
		return Media{}, err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return Media{}, err
	}

	var contentType string

	if fileInfo.IsDir() {
		contentType = "directory"
	} else {
		mimeType, err := mimetype.DetectFile(filename)
		if err != nil {
			contentType = "application/octet-stream"
		} else {
			contentType = mimeType.String()
		}
	}

	isAudio := strings.HasPrefix(contentType, "audio/")
	isVideo := strings.HasPrefix(contentType, "video/")
	isImage := strings.HasPrefix(contentType, "image/")

	var dimensions ImageDimensions
	var duration uint
	var hasSound bool

	if isImage {
		if contentType == "image/svg" || contentType == "image/svg+xml" {
			dimensions, err = GetSVGDimensions(file)
		} else {
			dimensions, err = GetImageDimensions(file)
		}
		if err != nil {
			return Media{}, err
		}
	}

	if isVideo {
		dimensions, duration, hasSound, err = AnalyzeVideo(filename)
		if err != nil {
			return Media{}, err
		}
	}

	if isAudio {
		duration = AnalyzeAudio(file)
		hasSound = true
	}

	return Media{
		ID:          slugify.Marshal(filepathBaseNoExt(filename), true),
		Alt:         embedDeclaration.Alt,
		Title:       embedDeclaration.Title,
		Source:      embedDeclaration.Source,
		Path:        ctx.RelativePathToMedia(embedDeclaration),
		ContentType: contentType,
		Dimensions:  dimensions,
		Duration:    duration,
		Size:        uint64(fileInfo.Size()),
		Attributes:  embedDeclaration.Attributes,
		HasSound:    hasSound,
	}, nil
}

func (ctx *RunContext) RelativePathToMedia(embedDeclaration MediaEmbedDeclaration) string {
	if ctx.Flags.Scattered {
		return path.Clean(path.Join(ctx.Config.ScatteredModeFolder, embedDeclaration.Source))
	} else {
		return path.Clean(embedDeclaration.Source)
	}
}

// AnalyzeAudio takes in an os.File and returns the duration of the audio file in seconds. If any error occurs the duration will be 0.
func AnalyzeAudio(file *os.File) uint {
	var duration uint
	decoder := mp3.NewDecoder(file)
	skipped := 0
	var frame mp3.Frame
	for {
		err := decoder.Decode(&frame, &skipped)
		if err != nil {
			break
		} else {
			duration += uint(frame.Duration().Seconds())
		}
	}
	return duration
}

// AnalyzeVideo returns an ImageDimensions struct with the video's height, width and aspect ratio and a duration in seconds.
func AnalyzeVideo(filename string) (dimensions ImageDimensions, duration uint, hasSound bool, err error) {
	probe, err := ffmpeg.DefaultConfiguration()
	if err != nil {
		return
	}
	_, video, err := probe.Probe(filename)
	if err != nil {
		return
	}
	for _, stream := range video.Streams {
		if stream.CodecType == "audio" {
			hasSound = true
		}
		if stream.CodecType == "video" {
			dimensions = ImageDimensions{
				Height:      stream.Height,
				Width:       stream.Width,
				AspectRatio: float32(stream.Width) / float32(stream.Height),
			}
		}
	}
	durationFloat, parseErr := strconv.ParseFloat(video.Format.Duration, 64)
	if parseErr != nil {
		err = fmt.Errorf("couldn't convert media duration %#v to number: %w", video.Format.Duration, parseErr)
		return
	}
	duration = uint(durationFloat)
	return
}

// AnalyzeAllMediae analyzes all the mediae from ParsedDescription's MediaEmbedDeclarations and returns analyzed mediae, ready for use as Work.Media.
func (ctx *RunContext) AnalyzeAllMediae(embedDeclarations map[string][]MediaEmbedDeclaration, currentDirectory string) (map[string][]Media, error) {
	if ctx.Flags.Scattered {
		currentDirectory = path.Join(currentDirectory, ctx.Config.ScatteredModeFolder)
	}
	analyzedMediae := make(map[string][]Media)
	analyzedMediaeBySource := make(map[string]Media)
	for language, mediae := range embedDeclarations {
		analyzedMediae[language] = make([]Media, 0)
		for _, media := range mediae {
			// Handle sources which are URLs
			if isValidURL(media.Source) {
				analyzedMedia := Media{
					Alt:        media.Alt,
					Title:      media.Title,
					Source:     media.Source,
					Online:     true,
					Attributes: media.Attributes,
				}
				analyzedMediae[language] = append(analyzedMediae[language], analyzedMedia)
				continue
			}

			// Compute absolute filepath to media
			var filename string
			if !filepath.IsAbs(media.Source) {
				filename, _ = filepath.Abs(path.Join(currentDirectory, media.Source))
			} else {
				filename = media.Source
			}

			// Skip already-analyzed
			if alreadyAnalyzedMedia, ok := analyzedMediaeBySource[filename]; ok {
				// Update fields independent of media.Source
				alreadyAnalyzedMedia.Alt = media.Alt
				alreadyAnalyzedMedia.Attributes = media.Attributes
				alreadyAnalyzedMedia.Title = media.Title
				analyzedMediae[language] = append(analyzedMediae[language], alreadyAnalyzedMedia)
				continue
			}

			ctx.Status(StepMediaAnalysis, ProgressDetails{
				File: filename,
			})
			analyzedMedia, err := ctx.AnalyzeMediaFile(filename, media)
			if err != nil {
				return map[string][]Media{}, err
			}
			analyzedMediae[language] = append(analyzedMediae[language], analyzedMedia)
			analyzedMediaeBySource[filename] = analyzedMedia
		}
	}
	return analyzedMediae, nil
}
