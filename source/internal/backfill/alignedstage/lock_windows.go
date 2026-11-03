package alignedstage

import "errors"

func lock(string) (func(), error) {
	return nil, errors.New("official minute staging currently requires macOS or Linux file locking")
}
