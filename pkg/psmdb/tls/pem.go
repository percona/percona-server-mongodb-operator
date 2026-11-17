package tls

import (
	"bytes"
	"encoding/pem"
	"reflect"
)

func decodePEMList(data []byte) []*pem.Block {
	blocks := []*pem.Block{}
	rest := data
	for {
		var p *pem.Block
		p, rest = pem.Decode(rest)
		if p == nil {
			break
		}
		blocks = append(blocks, p)
	}
	return blocks
}

func mergePEMBlocks(result []*pem.Block, toMerge []*pem.Block) ([]*pem.Block, error) {
	for _, block := range toMerge {
		if !hasBlock(result, block) {
			result = append(result, block)
		}
	}
	return result, nil
}

func hasBlock(data []*pem.Block, block *pem.Block) bool {
	for _, b := range data {
		if equalBlock(b, block) {
			return true
		}
	}
	return false
}

func equalBlock(a, b *pem.Block) bool {
	return bytes.Equal(a.Bytes, b.Bytes) &&
		reflect.DeepEqual(a.Headers, b.Headers) &&
		a.Type == b.Type
}

// EqualPEM reports whether a and b contain the same PEM blocks in the same order,
// ignoring encoding differences such as whitespace. Inputs without PEM blocks are never equal.
func EqualPEM(a, b []byte) bool {
	aBlocks := decodePEMList(a)
	bBlocks := decodePEMList(b)
	if len(aBlocks) == 0 || len(aBlocks) != len(bBlocks) {
		return false
	}
	for i := range aBlocks {
		if !equalBlock(aBlocks[i], bBlocks[i]) {
			return false
		}
	}
	return true
}

func MergePEM(target []byte, toMerge ...[]byte) ([]byte, error) {
	var err error
	targetBlocks := decodePEMList(target)
	for _, mergeData := range toMerge {
		mergeBlocks := decodePEMList(mergeData)
		targetBlocks, err = mergePEMBlocks(targetBlocks, mergeBlocks)
		if err != nil {
			return nil, err
		}
	}

	ca := []byte{}
	for _, block := range targetBlocks {
		ca = append(ca, pem.EncodeToMemory(block)...)
	}

	return ca, nil
}
