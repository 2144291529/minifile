package app

import "io/fs"

func fsSub(fsys fs.FS, dir string) (fs.FS, error) {
	return fs.Sub(fsys, dir)
}
