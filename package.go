package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
)

// PreinstallPackage represents a package which has not yet been installed.
// Setting the Name and Version is required.
type PreinstallPackage struct {
	PreinstallPackageOpts
	// Id does NOT uniquely identify the package (it might, but it might not). Use FullPath instead.
	Id string
	// FullPath is the result of joining the Name, Version and Id with filepath. Can be used to uniquely identify the package.
	// TODO: this is quite intuitive, but might seem a bit weird.
	FullPath    string
	Initialised bool

	// tarballPath is the location of the tarball either provided or downloaded remotely.
	tarballPath string
	// tarballReader is the byte reader for the downloaded tarball.
	tarballReader io.ReadCloser
}

// PreinstallPackageOpts specifies the required options to initialise a PreinstallPackage.
type PreinstallPackageOpts struct {
	Name    string
	Version string
	// UseDisk determines whether the archive will be downloaded to a temp file, then extracted, or downloaded and extracted in-memory.
	// Recommended to set to false unless you have limited RAM.
	UseDisk bool
	// RetainTarball specifies whether the tarball used during installation is kept afterwards.
	// You likely want to set this to true if installing from a local file.
	RetainTarball bool
}

// setOpts finalises a package's metadata, preparing it for installation.
// Expects Name and Version of opts to be set.
func (p *PreinstallPackage) setOpts(opts PreinstallPackageOpts) error {
	if opts.Version == "" || opts.Name == "" {
		return errors.New("name and version must be non-empty to initialise PreinstallPackage")
	}

	p.PreinstallPackageOpts = opts
	p.Id = generateId()
	p.FullPath = filepath.Join(p.Name, p.Version, p.Id)

	return nil
}

// NewPackageFromRemote downloads a tarball from a remote URL and finalises its metadata, preparing it for installation.
// The caller should always run Cleanup to delete the tarball AFTER installation.
func NewPackageFromRemote(tarballUrl string, opts PreinstallPackageOpts) (*PreinstallPackage, error) {
	p := &PreinstallPackage{}
	if err := p.setOpts(opts); err != nil {
		return nil, err
	}

	if opts.UseDisk {
		if !p.RetainTarball {
			slog.Warn("downloading to disk and NOT deleting the temporary archive after installation", "url", tarballUrl)
		}

		slog.Info("remote download: downloading archive to disk", "url", tarballUrl)
		tarballPath, err := p.downloadRemote(tarballUrl)
		if err != nil {
			return nil, err
		}

		p.tarballPath = tarballPath
		reader, err := os.Open(tarballPath)
		if err != nil {
			slog.Error("failed to open reader for tarball on disk")
			return nil, err
		}
		p.tarballReader = reader

		slog.Debug("temp file reader set up, ready for initialisation", "tarballPath", tarballPath)
	} else {
		slog.Info("remote download: reading archive into memory", "url", tarballUrl)
		reader, err := p.readRemote(tarballUrl)
		if err != nil {
			return nil, err
		}
		p.tarballReader = reader

		slog.Debug("remote reader set up, ready for initialisation")
	}

	// TODO: could turn this into a Ready function to check dynamically.
	p.Initialised = true
	return p, nil
}

func NewPackageFromFile(fp string, opts PreinstallPackageOpts) (*PreinstallPackage, error) {
	p := &PreinstallPackage{}
	if err := p.setOpts(opts); err != nil {
		return nil, err
	}
	if !p.RetainTarball {
		slog.Warn("local file: the tarball provided will be deleted after installation", "path", fp)
	}

	slog.Info("local file: opening file")
	reader, err := os.Open(fp)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Error("local file: file does not exist", "path", fp)
			return nil, err
		}

		slog.Error("local file: failed to open, do we have permission?", "path", fp)
		return nil, err
	}

	p.tarballPath = fp
	p.tarballReader = reader
	p.Initialised = true
	return p, nil
}

// Cleanup should always be called after installation, or on error. Deletes the tarball used during installation
// and closes any readers. See PreinstallPackage.RetainTarball.
func (p *PreinstallPackage) Cleanup() {
	slog.Info("post-installation cleanup", "package", p.Name)
	if !p.RetainTarball {
		os.Remove(p.tarballPath)
	}
	if p.tarballReader != nil {
		p.tarballReader.Close()
	}
}

// downloadRemote downloads a tarball from a remote URL and returns the path to the temporary file.
func (p *PreinstallPackage) downloadRemote(tarballUrl string) (string, error) {
	tempFile, err := os.CreateTemp("", generateId()+path.Base(tarballUrl))
	if err != nil {
		slog.Error("failed to create temporary file for remote download")
		return "", err
	}

	body, err := p.readRemote(tarballUrl)
	if err != nil {
		return "", err
	}
	defer body.Close()

	_, err = io.Copy(tempFile, body)
	if err != nil {
		slog.Error("failed to write tarball to a temporary file")
		return "", err
	}

	return tempFile.Name(), nil
}

// readRemote GETs the tarball from the remote URL and returns the Body as a ReadCloser.
func (p *PreinstallPackage) readRemote(tarballUrl string) (io.ReadCloser, error) {
	resp, err := http.Get(tarballUrl)
	if err != nil {
		slog.Error("failed to GET tarball from remote server")
		return nil, err
	}

	return resp.Body, nil
}

// Package represents a package that is installed.
type Package struct {
	*PreinstallPackage // copy to avoid issues with changing PreinstallPackage mid install
	// Path is the location of the package in the store, i.e. its actual location before symlinking.
	Path string
	// Symlinked is whether the package has been symlinked from the store to ~/.local, etc.
	Symlinked bool
}

// Install installs a package to the given storePath. If interactive is false, this will skip printing
// some information and won't ask questions.
// This should not usually be called directly. Instead, use PackageManager.Install.
func (ppkg *PreinstallPackage) Install(storePath string, interactive bool) (*Package, error) {
	if !ppkg.Initialised {
		return nil, errors.New("package is not initialised; has Init been called?")
	}

	pkg := &Package{
		PreinstallPackage: ppkg,
		Path:              filepath.Join(storePath, ppkg.FullPath),
		Symlinked:         false,
	}
	if err := os.MkdirAll(pkg.Path, 0755); err != nil {
		slog.Error("failed to create package directory", "path", pkg.Path)
		return nil, err
	}

	slog.Info("extracting archive", "package", pkg.Name, "path", pkg.Path)
	if err := tarExtract(pkg.tarballReader, pkg.Path); err != nil {
		return nil, err
	}
	// TODO: call cleanup here
	// TODO: fixing directory structure
	// TODO: symlinking

	return pkg, nil
}

type PackageManager struct {
	PackageManagerOpts
	Initialised bool
}

type PackageManagerOpts struct {
	StorePath   string
	Interactive bool
}

func NewPackageManager(opts PackageManagerOpts) (*PackageManager, error) {
	if opts.StorePath == "" {
		return nil, errors.New("a StorePath must be provided to create a new package manager")
	}

	pm := &PackageManager{
		PackageManagerOpts: opts,
	}
	if err := pm.Init(); err != nil {
		slog.Error("failed to initialise PackageManager")
		return nil, err
	}
	return pm, nil
}

// Init initialises the package manager, creating all needed directories.
// Should usually not be called directly. Call newPackageManager instead.
func (pm *PackageManager) Init() error {
	if err := os.MkdirAll(pm.StorePath, 0755); err != nil {
		slog.Error("failed to create store directory, do we have permission?", "storePath", pm.StorePath)
		return err
	}

	// TODO: more stuff for initialisation

	slog.Info("package manager has been initialised", "storePath", pm.StorePath)
	pm.Initialised = true
	return nil
}

func (pm *PackageManager) Install(ppkg *PreinstallPackage) (*Package, error) {
	if !pm.Initialised {
		return nil, errors.New("package manager was not initialised. was Init called?")
	}
	return ppkg.Install(pm.StorePath, pm.Interactive)
}
