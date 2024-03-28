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
		if bytes.Equal(b.Bytes, block.Bytes) &&
			reflect.DeepEqual(b.Headers, block.Headers) &&
			b.Type == block.Type {
			return true
		}
	}
	return false
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
