package util

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path"
)

func Unzip(zipPath string, p string) error {

	log.Printf("Unzipping %s to %s", zipPath, p)

	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}

	// extract zip
	for _, f := range archive.File {
		log.Printf("Extracting %s", f.Name)

		if f.FileInfo().IsDir() {
			path := path.Join(p, f.Name)
			log.Printf("Creating directory %s in %s", f.Name, path)

			err = os.MkdirAll(path, 0777)
			if err != nil {
				return err
			}
			continue
		}

		// open file
		rc, err := f.Open()
		if err != nil {
			return err
		}

		// create file
		path := path.Join(p, f.Name)
		// err = os.MkdirAll(path, 0777)
		// if err != nil {
		// return err
		// }

		// write file
		w, err := os.Create(path)
		if err != nil {
			return err
		}

		// copy
		_, err = io.Copy(w, rc)
		if err != nil {
			return err
		}

		log.Printf("Extracted %s to %s", f.Name, path)

		// close
		rc.Close()
		w.Close()
	}

	return nil
}

// EncodeDir takes all files in the input directory,
// compresses them into a temporary zip archive,
// and returns the base64 encoding of that archive.
// Caveat: it doesn't (yet) recursively go through the directory.
// Hence, when deploying functions from multiple files,
// don't nest them in subdirectories.
// TODO check whether tinyfaas could support that (i.e. multiple files for a function)
func EncodeDir(functionDirPath string) (string, error) {

	functionDirName := path.Dir(functionDirPath)

	// create a temporary zip archive to copy the function code + requirements into
	archive, err := os.CreateTemp("", functionDirName+".zip")
	if err != nil {
		return "", err
	}
	defer archive.Close()
	defer os.Remove(archive.Name())

	// create new zip writer
	writer := zip.NewWriter(archive)
	fmt.Println(archive.Name())

	// iterate over files in dir and copy into zip
	dir, _ := os.Open(functionDirPath)
	defer dir.Close()
	filenames, _ := dir.ReadDir(-1)
	for _, fileinfo := range filenames {
		// open file
		file, err := os.Open(path.Join(functionDirPath, fileinfo.Name()))
		if err != nil {
			return "", err
		}
		// copy into zip
		w, err := writer.Create(fileinfo.Name())
		if err != nil {
			return "", nil
		}
		if _, err := io.Copy(w, file); err != nil {
			return "", nil
		}
		// close file
		err = file.Close()
		if err != nil {
			return "", err
		}
	}

	// close the writer
	err = writer.Close()
	if err != nil {
		return "", err
	}

	// encode zip in base64 => return that string
	archiveByes, err := os.ReadFile(archive.Name())
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(archiveByes), nil
}
