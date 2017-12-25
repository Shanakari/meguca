package db

import (
	"database/sql"
	"errors"
	"meguca/auth"
	"meguca/common"
	"meguca/imager/assets"
	"meguca/util"
	"time"

	"github.com/lib/pq"
)

const (
	// Time it takes for an image allocation token to expire
	tokenTimeout = time.Minute
)

var (
	// ErrInvalidToken occurs, when trying to retrieve an image with an
	// non-existent token. The token might have expired (60 to 119 seconds) or
	// the client could have provided an invalid token to begin with.
	ErrInvalidToken = errors.New("invalid image token")
)

// WriteImage writes a processed image record to the DB
func WriteImage(tx *sql.Tx, i common.ImageCommon) error {
	dims := pq.GenericArray{A: i.Dims}
	_, err := getStatement(tx, "write_image").Exec(
		i.APNG, i.Audio, i.Video, i.FileType, i.ThumbType, dims, i.Length,
		i.Size, i.MD5, i.SHA1, i.Title, i.Artist,
	)
	return err
}

// GetImage retrieves a thumbnailed image record from the DB
func GetImage(SHA1 string) (common.ImageCommon, error) {
	return scanImage(prepared["get_image"].QueryRow(SHA1))
}

func scanImage(rs rowScanner) (img common.ImageCommon, err error) {
	var scanner imageScanner
	err = rs.Scan(scanner.ScanArgs()...)
	if err != nil {
		return
	}
	return scanner.Val().ImageCommon, nil
}

// NewImageToken inserts a new image allocation token into the DB and returns
// it's ID
func NewImageToken(SHA1 string) (token string, err error) {
	// Loop in case there is a primary key collision
	for {
		token, err = auth.RandomID(64)
		if err != nil {
			return
		}
		expires := time.Now().Add(tokenTimeout)

		err = execPrepared("write_image_token", token, SHA1, expires)
		switch {
		case err == nil:
			return
		case IsConflictError(err):
			continue
		default:
			return
		}
	}
}

// UseImageToken deletes an image allocation token and returns the matching
// processed image. If no token exists, returns ErrInvalidToken.
func UseImageToken(tx *sql.Tx, token string) (
	img common.ImageCommon, err error,
) {
	if len(token) != common.LenImageToken {
		err = ErrInvalidToken
		return
	}

	var SHA1 string
	err = tx.Stmt(prepared["use_image_token"]).QueryRow(token).Scan(&SHA1)
	if err != nil {
		return
	}

	img, err = scanImage(tx.Stmt(prepared["get_image"]).QueryRow(SHA1))
	return
}

// AllocateImage allocates an image's file resources to their respective served
// directories and write its data to the database
func AllocateImage(src, thumb []byte, img common.ImageCommon) error {
	err := assets.Write(img.SHA1, img.FileType, img.ThumbType, src, thumb)
	if err != nil {
		return cleanUpFailedAllocation(img, err)
	}

	err = WriteImage(nil, img)
	if err != nil {
		return cleanUpFailedAllocation(img, err)
	}
	return nil
}

// Delete any dangling image files in case of a failed image allocation
func cleanUpFailedAllocation(img common.ImageCommon, err error) error {
	delErr := assets.Delete(img.SHA1, img.FileType, img.ThumbType)
	if delErr != nil {
		err = util.WrapError(err.Error(), delErr)
	}
	return err
}

// HasImage returns, if the post has an image allocated. Only used in tests.
func HasImage(id uint64) (has bool, err error) {
	err = db.
		QueryRow(`
			SELECT true FROM posts
				WHERE id = $1 AND SHA1 IS NOT NULL`,
			id,
		).
		Scan(&has)
	if err == sql.ErrNoRows {
		err = nil
	}
	return
}

// InsertImage insert and image into and existing open post
func InsertImage(tx *sql.Tx, id uint64, img common.Image) error {
	_, err := getStatement(tx, "insert_image").Exec(id, img.SHA1, img.Name)
	return err
}

// SpoilerImage spoilers an already allocated image
func SpoilerImage(id uint64) error {
	return execPrepared("spoiler_image", id)
}
