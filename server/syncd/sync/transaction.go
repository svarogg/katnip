package sync

import (
	"encoding/hex"

	"github.com/kaspanet/kaspad/app/appmessage"
	"github.com/kaspanet/kaspad/domain/consensus/model/externalapi"
	"github.com/kaspanet/kaspad/domain/consensus/utils/subnetworks"
	"github.com/kaspanet/kaspad/infrastructure/logger"
	"github.com/pkg/errors"
	"github.com/someone235/katnip/server/database"
	"github.com/someone235/katnip/server/dbaccess"
	"github.com/someone235/katnip/server/dbmodels"
	"github.com/someone235/katnip/server/serializer"
)

type txWithMetadata struct {
	tx    *appmessage.RPCTransaction
	id    uint64
	isNew bool
}

func transactionHashesToTxsWithMetadataToTransactionHashes(transactionHashesToTxsWithMetadata map[string]*txWithMetadata) []string {
	hashes := make([]string, len(transactionHashesToTxsWithMetadata))
	i := 0
	for hash := range transactionHashesToTxsWithMetadata {
		hashes[i] = hash
		i++
	}
	return hashes
}

func insertTransactions(dbTx *database.TxContext, blocks []*appmessage.RPCBlock, subnetworkIDsToIDs map[string]uint64) (
	map[string]*txWithMetadata, error) {

	onEnd := logger.LogAndMeasureExecutionTime(log, "insertTransactions")
	defer onEnd()

	transactionHashesToTxsWithMetadata := make(map[string]*txWithMetadata)
	for _, block := range blocks {
		// We do not directly iterate over block.Verbose.RawTx because it is a slice of values, and iterating
		// over such will re-use the same address, making all pointers pointing into it point to the same address
		for i := range block.Transactions {
			transaction := block.Transactions[i]
			transactionHashesToTxsWithMetadata[transaction.VerboseData.Hash] = &txWithMetadata{
				tx: transaction,
			}
		}
	}

	transactionHashes := transactionHashesToTxsWithMetadataToTransactionHashes(transactionHashesToTxsWithMetadata)

	dbTransactions, err := dbaccess.TransactionsByHashes(dbTx, transactionHashes)
	if err != nil {
		return nil, err
	}

	for _, dbTransaction := range dbTransactions {
		transactionHashesToTxsWithMetadata[dbTransaction.TransactionHash].id = dbTransaction.ID
	}

	newTransactionHashes := make([]string, 0)
	for hash, transaction := range transactionHashesToTxsWithMetadata {
		if transaction.id != 0 {
			continue
		}
		newTransactionHashes = append(newTransactionHashes, hash)
	}

	transactionsToAdd := make([]interface{}, len(newTransactionHashes))
	for i, hash := range newTransactionHashes {
		tx := transactionHashesToTxsWithMetadata[hash].tx

		payload, err := hex.DecodeString(tx.Payload)
		if err != nil {
			return nil, err
		}

		subnetworkID, ok := subnetworkIDsToIDs[tx.SubnetworkID]
		if !ok {
			return nil, errors.Errorf("couldn't find ID for subnetwork %s", tx.SubnetworkID)
		}

		transactionsToAdd[i] = &dbmodels.Transaction{
			TransactionHash: tx.VerboseData.Hash,
			TransactionID:   tx.VerboseData.TransactionID,
			LockTime:        serializer.Uint64ToBytes(tx.LockTime),
			SubnetworkID:    subnetworkID,
			Gas:             tx.Gas,
			Payload:         payload,
			Version:         tx.Version,
		}
	}

	err = dbaccess.BulkInsert(dbTx, transactionsToAdd)
	if err != nil {
		return nil, err
	}

	dbNewTransactions, err := dbaccess.TransactionsByHashes(dbTx, newTransactionHashes)
	if err != nil {
		return nil, err
	}

	if len(dbNewTransactions) != len(newTransactionHashes) {
		return nil, errors.New("couldn't add all new transactions")
	}

	for _, dbTransaction := range dbNewTransactions {
		transactionHashesToTxsWithMetadata[dbTransaction.TransactionHash].id = dbTransaction.ID
		transactionHashesToTxsWithMetadata[dbTransaction.TransactionHash].isNew = true
	}

	return transactionHashesToTxsWithMetadata, nil
}

func convertTxRawResultToMsgTx(tx *appmessage.RPCTransaction) (*appmessage.MsgTx, error) {
	txIns := make([]*appmessage.TxIn, len(tx.Inputs))
	for i, txIn := range tx.Inputs {
		prevTxID, err := externalapi.NewDomainTransactionIDFromString(txIn.PreviousOutpoint.TransactionID)
		if err != nil {
			return nil, err
		}
		signatureScript, err := hex.DecodeString(txIn.SignatureScript)
		if err != nil {
			return nil, err
		}
		txIns[i] = &appmessage.TxIn{
			PreviousOutpoint: appmessage.Outpoint{
				TxID:  *prevTxID,
				Index: txIn.PreviousOutpoint.Index,
			},
			SignatureScript: signatureScript,
			Sequence:        txIn.Sequence,
		}
	}
	txOuts := make([]*appmessage.TxOut, len(tx.Outputs))
	for i, txOut := range tx.Outputs {
		scriptPubKey, err := hex.DecodeString(txOut.ScriptPublicKey.Script)
		if err != nil {
			return nil, err
		}
		txOuts[i] = &appmessage.TxOut{
			Value: txOut.Amount,
			ScriptPubKey: &externalapi.ScriptPublicKey{
				Script:  scriptPubKey,
				Version: txOut.ScriptPublicKey.Version, // TODO: Update it with real version
			},
		}
	}
	subnetworkID, err := subnetworks.FromString(tx.SubnetworkID)
	if err != nil {
		return nil, err
	}
	if subnetworkID.Equal(&subnetworks.SubnetworkIDNative) {
		return appmessage.NewNativeMsgTx(tx.Version, txIns, txOuts), nil
	}
	payload, err := hex.DecodeString(tx.Payload)
	if err != nil {
		return nil, err
	}
	return appmessage.NewSubnetworkMsgTx(tx.Version, txIns, txOuts, subnetworkID, tx.Gas, payload), nil
}
