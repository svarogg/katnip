package sync

import (
	"github.com/kaspanet/kaspad/app/appmessage"
	"strconv"

	"github.com/kaspanet/kaspad/util/mstime"
	"github.com/kaspanet/kasparov/database"
	"github.com/kaspanet/kasparov/serializer"

	"github.com/kaspanet/kasparov/dbaccess"
	"github.com/kaspanet/kasparov/dbmodels"
	"github.com/pkg/errors"
)

func insertBlocks(dbTx *database.TxContext, blocks []*appmessage.BlockVerboseData) error {
	blocksToAdd := make([]interface{}, len(blocks))
	for i, block := range blocks {
		var err error
		blocksToAdd[i], err = dbBlockFromVerboseBlock(block)
		if err != nil {
			return err
		}
	}
	return dbaccess.BulkInsert(dbTx, blocksToAdd)
}

func getBlocksWithTheirParentIDs(dbTx *database.TxContext, blocks []*appmessage.BlockVerboseData) (map[string]uint64, error) {
	blockSet := make(map[string]struct{})
	for _, block := range blocks {
		blockSet[block.Hash] = struct{}{}
		for _, parentHash := range block.ParentHashes {
			blockSet[parentHash] = struct{}{}
		}
	}

	blockHashes := stringsSetToSlice(blockSet)

	dbBlocks, err := dbaccess.BlocksByHashes(dbTx, blockHashes)
	if err != nil {
		return nil, err
	}

	if len(dbBlocks) != len(blockSet) {
		return nil, errors.Errorf("couldn't retrieve all block IDs")
	}

	blockHashesToIDs := make(map[string]uint64)
	for _, dbBlock := range dbBlocks {
		blockHashesToIDs[dbBlock.BlockHash] = dbBlock.ID
	}
	return blockHashesToIDs, nil
}

func getNonExistingBlocks(dbTx *database.TxContext, getBlocksResponse *appmessage.GetBlocksResponseMessage) ([]*appmessage.BlockVerboseData, error) {
	existingBlockHashes, err := dbaccess.ExistingHashes(dbTx, getBlocksResponse.BlockHashes)
	if err != nil {
		return nil, err
	}

	existingBlockHashesSet := make(map[string]struct{}, len(existingBlockHashes))
	for _, hash := range existingBlockHashes {
		existingBlockHashesSet[hash] = struct{}{}
	}

	nonExistingBlocks := make([]*appmessage.BlockVerboseData, 0, len(getBlocksResponse.BlockVerboseData))
	for _, block := range getBlocksResponse.BlockVerboseData {
		if _, exists := existingBlockHashesSet[block.Hash]; exists {
			continue
		}

		nonExistingBlocks = append(nonExistingBlocks, block)
	}

	return nonExistingBlocks, nil
}

func dbBlockFromVerboseBlock(verboseBlock *appmessage.BlockVerboseData) (*dbmodels.Block, error) {
	bits, err := strconv.ParseUint(verboseBlock.Bits, 16, 32)
	if err != nil {
		return nil, err
	}

	dbBlock := dbmodels.Block{
		BlockHash:            verboseBlock.Hash,
		Version:              verboseBlock.Version,
		HashMerkleRoot:       verboseBlock.HashMerkleRoot,
		AcceptedIDMerkleRoot: verboseBlock.AcceptedIDMerkleRoot,
		UTXOCommitment:       verboseBlock.UTXOCommitment,
		Timestamp:            mstime.UnixMilliseconds(verboseBlock.Time).ToNativeTime(),
		Bits:                 uint32(bits),
		Nonce:                serializer.Uint64ToBytes(verboseBlock.Nonce),
		BlueScore:            verboseBlock.BlueScore,
		IsChainBlock:         false, // This must be false for updateSelectedParentChain to work properly
		TransactionCount:     uint16(len(verboseBlock.TransactionVerboseData)),
		Difficulty:           verboseBlock.Difficulty,
	}

	// Set genesis block as the initial chain block
	if len(verboseBlock.ParentHashes) == 0 {
		dbBlock.IsChainBlock = true
	}
	return &dbBlock, nil
}
