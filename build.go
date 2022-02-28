// Package ortfodb exposes the various functions used by the ortfodb portfolio database creation command-line tool.
// It is notably used by ortfomk to share some common data between the two complementing programs.
// See https://ewen.works/ortfodb for more information.
package ortfodb

import (
	"errors"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"time"

	"path"

	jsoniter "github.com/json-iterator/go"
	"gopkg.in/yaml.v2"
)

// RunContext holds several "global" references used throughout all the functions of a command.
type RunContext struct {
	Config *Configuration
	// ID of the work currently being processed.
	CurrentWorkID     string
	DatabaseDirectory string
	Flags             Flags
	Progress          struct {
		Current int
		Total   int
	}
}

type Flags struct {
	Scattered bool
	Silent    bool
	Minified  bool
	Config    string
}

// Project represents a project.
type Project struct {
	ID             string
	DescriptionRaw string
	Description    ParsedDescription
	Ctx            *RunContext
}

// Build builds the database at outputFilename from databaseDirectory.
// Use LoadConfiguration (and ValidateConfiguration if desired) to get a Configuration.
func Build(databaseDirectory string, outputFilename string, flags Flags, config Configuration) error {
	// defer fmt.Print("\033[2K\r\n")
	ctx := RunContext{
		Config:            &config,
		Flags:             flags,
		DatabaseDirectory: databaseDirectory,
	}
	err := os.MkdirAll(config.Media.At, 0o755)
	if err != nil {
		return fmt.Errorf("while creating the media output directory: %w", err)
	}

	works := make([]Work, 0)
	workDirectories := make([]fs.DirEntry, 0)
	databaseFiles, err := os.ReadDir(ctx.DatabaseDirectory)
	if err != nil {
		return err
	}

	// Build up workDirectories by filtering through databaseFiles.
	// We do this beforehand to compute ctx.Progress.Total.
	for _, dirEntry := range databaseFiles {
		// TODO: setting to ignore/allow “dotfolders”

		dirEntryAbsPath := path.Join(ctx.DatabaseDirectory, dirEntry.Name())
		if !dirEntry.IsDir() {
			continue
		}
		// Compute the description file's path
		var descriptionFilename string
		if ctx.Flags.Scattered {
			descriptionFilename = path.Join(dirEntryAbsPath, ctx.Config.ScatteredModeFolder, "description.md")
		} else {
			descriptionFilename = path.Join(dirEntryAbsPath, "description.md")
		}
		// If it's not there, this directory is not a project worth scanning.
		if _, err := os.Stat(descriptionFilename); os.IsNotExist(err) {
			continue
		}

		workDirectories = append(workDirectories, dirEntry)
	}

	ctx.Progress.Total = len(workDirectories)

	for _, dirEntry := range workDirectories {
		dirEntryAbsPath := path.Join(ctx.DatabaseDirectory, dirEntry.Name())

		workID := dirEntry.Name()

		// Compute the description file's path
		var descriptionFilename string
		if ctx.Flags.Scattered {
			descriptionFilename = path.Join(dirEntryAbsPath, ctx.Config.ScatteredModeFolder, "description.md")
		} else {
			descriptionFilename = path.Join(dirEntryAbsPath, "description.md")
		}

		// Update the UI
		ctx.CurrentWorkID = workID
		ctx.Progress.Current++

		// Parse the description
		descriptionRaw, err := ioutil.ReadFile(descriptionFilename)
		if err != nil {
			return err
		}
		description := ctx.ParseDescription(string(descriptionRaw))

		// Analyze mediae
		analyzedMediae, err := ctx.AnalyzeAllMediae(description.MediaEmbedDeclarations, dirEntryAbsPath)
		if err != nil {
			return err
		}

		// Copy over the media
		if config.Media.At == "" {
			return errors.New("please specify a destination for the media files in the configuration file (set media.at)")
		}

		for _, mediae := range analyzedMediae {
			for _, media := range mediae {
				absolutePath := path.Join(dirEntryAbsPath, media.Path)
				content, err := os.ReadFile(absolutePath)
				if err != nil {
					fmt.Printf("could not copy %s to %s: %v\n", absolutePath, config.Media.At, err)
				}
				err = os.MkdirAll(path.Dir(ctx.AbsolutePathToMedia(media)), 0o755)
				if err != nil {
					return fmt.Errorf("could not create output directory for %s: %w", ctx.AbsolutePathToMedia(media), err)
				}

				err = os.WriteFile(ctx.AbsolutePathToMedia(media), content, 0777)
				if err != nil {
					fmt.Printf("could not copy %s to %s: %v\n", absolutePath, config.Media.At, err)
				}
			}
		}

		// Make thumbnails
		// TODO: do only one loop for media, and do color extraction, thumb creation and copy at once, instead of iterating separately three times
		// TODO: Color extraction comes after since it could take advantage of built thumbs to sample the color:
		// - faster (it takes the smallest image)
		// - for more content types (PDFs and videos cannot be used directly, but thumbnails of them can)
		metadata := description.Metadata
		if config.MakeThumbnails.Enabled {
			ctx.Status("Making thumbnails")
			metadata, err = ctx.StepMakeThumbnails(metadata, workID, analyzedMediae)
			if err != nil {
				return err
			}
		}

		// Extract colors
		if config.ExtractColors.Enabled {
			ctx.Status("Extracting colors")
			// Build up the array of media paths
			// TODO: include thumbnails instead
			mediaPaths := make([]string, 0)
			for _, mediaeInOneLang := range analyzedMediae {
				for _, media := range mediaeInOneLang {
					mediaPaths = append(mediaPaths, ctx.AbsolutePathToMedia(media))
				}
			}
			metadata = ctx.StepExtractColors(metadata, mediaPaths)
		}

		// Return the finished work
		work := Work{
			ID:         workID,
			Metadata:   metadata,
			Title:      description.Title,
			Paragraphs: description.Paragraphs,
			Media:      analyzedMediae,
			Links:      description.Links,
			Footnotes:  description.Footnotes,
		}
		works = append(works, work)
	}

	// Compile the database
	var worksJSON []byte
	json := jsoniter.ConfigFastest
	setJSONNamingStrategy(lowerCaseWithUnderscores)
	if flags.Minified {
		worksJSON, _ = json.Marshal(works)
	} else {
		worksJSON, _ = json.MarshalIndent(works, "", "    ")
	}

	// Output it
	err = writeFile(outputFilename, worksJSON)
	if !flags.Silent {
		fmt.Print("\033[2K\r\n")
		println(string(worksJSON))
	}
	if err != nil {
		println(err.Error())
	}

	// Update the the build metadata file
	err = config.UpdateBuildMetadata()
	if err != nil {
		println(err.Error())
	}
	return nil
}

// GetProjectPath returns the project's folder path with regard to databaseDirectory.
func (p *Project) ProjectPath() string {
	if p.Ctx.Flags.Scattered {
		return path.Join(p.Ctx.DatabaseDirectory, p.ID, p.Ctx.Config.ScatteredModeFolder)
	}
	return path.Join(p.Ctx.DatabaseDirectory, p.ID)
}

// ReadDescriptionFile reads the description.md file in directory.
// Returns an empty string if the file is a directory or does not exist.
func ReadDescriptionFile(directory string) (string, error) {
	descriptionFilepath := path.Join(directory, "description.md")
	if !fileExists(descriptionFilepath) {
		return "", nil
	}
	descriptionFile, err := os.Stat(descriptionFilepath)
	if err != nil {
		return "", err
	}
	if descriptionFile.IsDir() {
		return "", nil
	}
	return readFile(descriptionFilepath)
}

// UpdateBuildMetadata updates metadata about the latest build in config.BuildMetadataFilepath.
// If the file does not exist, it creates it.
func (config Configuration) UpdateBuildMetadata() (err error) {
	var metadata BuildMetadata
	if _, err = os.Stat(config.BuildMetadataFilepath); errors.Is(err, os.ErrNotExist) {
		os.MkdirAll(path.Dir(config.BuildMetadataFilepath), os.ModePerm)
		metadata = BuildMetadata{}
	} else {
		metadata, err = config.BuildMetadata()
		if err != nil {
			return
		}
	}
	metadata.PreviousBuildDate = time.Now()
	raw, err := yaml.Marshal(&metadata)
	if err != nil {
		return
	}
	err = writeFile(config.BuildMetadataFilepath, raw)
	return
}

func (config Configuration) BuildMetadata() (metadata BuildMetadata, err error) {
	raw, err := readFileBytes(config.BuildMetadataFilepath)
	if err != nil {
		return
	}
	err = yaml.Unmarshal(raw, &metadata)
	return
}

// NeedsRebuiling returns true if the given path has its modified date sooner than the last build's date.
// If any error occurs, the result is true (ie 'this file needs to be rebuilt').
func (ctx *RunContext) NeedsRebuiling(absolutePath string) bool {
	metadata, err := ctx.Config.BuildMetadata()
	if err != nil {
		return true
	}
	fileMeta, err := os.Stat(absolutePath)
	if err != nil {
		return true
	}
	return fileMeta.ModTime().After(metadata.PreviousBuildDate)
}
