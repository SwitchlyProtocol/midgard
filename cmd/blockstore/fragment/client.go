// This client will only catch the Block endpoint from Tendermint for fragment module
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/switchlyprotocol/midgard/config"
	"github.com/switchlyprotocol/midgard/internal/util/miderr"
	"github.com/switchlyprotocol/midgard/internal/util/midlog"
)

var logger = midlog.LoggerForModule("chain")

// Block is a chain record.
type Block struct {
	Height    int64
	PureBlock *coretypes.ResultBlock `json:"pure_block"`
}

// Client provides Tendermint access.
type Client struct {
	ctx context.Context

	// Single RPC access
	client *rpchttp.HTTP

	// Parallel / batched access
	batchClients []*rpchttp.BatchHTTP

	batchSize   int // divisible by parallelism
	parallelism int
}

func (c *Client) BatchSize() int {
	return c.batchSize
}

// NewClient configures a new instance. Timeout applies to all requests on endpoint.
func NewClient(ctx context.Context) (*Client, error) {
	cfg := &config.Global
	var timeout time.Duration = cfg.Switchly.ReadTimeout.Value()

	endpoint, err := url.Parse(cfg.Switchly.TendermintURL)
	if err != nil {
		logger.FatalE(err, "Exit on malformed Tendermint RPC URL")
	}

	batchSize := cfg.Switchly.FetchBatchSize
	parallelism := cfg.Switchly.Parallelism
	if batchSize%parallelism != 0 {
		logger.FatalF("BatchSize=%d must be divisible by Parallelism=%d", batchSize, parallelism)
	}

	// need the path separate from the URL for some reason
	path := endpoint.Path
	endpoint.Path = ""
	remote := endpoint.String()

	var client *rpchttp.HTTP
	var batchClients []*rpchttp.BatchHTTP
	for i := 0; i < parallelism; i++ {
		// rpchttp.NewWithTimeout rounds to seconds for some reason
		client, err = rpchttp.NewWithClient(remote, path, &http.Client{Timeout: timeout})
		if err != nil {
			return nil, fmt.Errorf("tendermint RPC client instantiation: %w", err)
		}
		batchClients = append(batchClients, client.NewBatch())
	}

	return &Client{
		ctx:          ctx,
		client:       client,
		batchClients: batchClients,
		batchSize:    batchSize,
		parallelism:  parallelism,
	}, nil
}

// Fetch the summary of the chain: latest height, node address, ...
func (c *Client) RefreshStatus() (rs *coretypes.ResultStatus, err error) {
	cfg := &config.Global
	for i := 0; i < cfg.Switchly.MaxStatusRetries; i++ {
		rs, err = c.client.Status(c.ctx)
		if err == nil {
			return rs, nil
		}
		logger.ErrorE(err, "tendermint RPC status")
		time.Sleep(time.Duration(cfg.Switchly.StatusRetryBackoff))
	}
	return nil, fmt.Errorf("tendermint RPC status: %w", err)
}

type Iterator struct {
	c                *Client
	nextBatchStart   int64
	finalBlockHeight int64
	batch            []Block
}

func (c *Client) Iterator(startHeight, finalBlockHeight int64) Iterator {
	return Iterator{
		c:                c,
		nextBatchStart:   startHeight,
		finalBlockHeight: finalBlockHeight,
	}
}

func (i *Iterator) Next() (*Block, error) {
	if len(i.batch) == 0 {
		hasMore, err := i.nextBatch()
		if err != nil || !hasMore {
			return nil, err
		}
	}
	ret := &i.batch[0]
	i.batch = i.batch[1:]
	return ret, nil
}

func (i *Iterator) nextBatch() (hasMore bool, err error) {
	if len(i.batch) != 0 {
		return false, miderr.InternalErr("Batch still filled")
	}
	if i.finalBlockHeight < i.nextBatchStart {
		return false, nil
	}

	batchSize := int64(i.c.batchSize)
	parallelism := i.c.parallelism

	remainingOnChain := i.finalBlockHeight - i.nextBatchStart + 1
	if remainingOnChain < batchSize {
		batchSize = remainingOnChain
		parallelism = 1
	}
	i.batch = make([]Block, batchSize)
	err = i.c.fetchBlocksParallel(i.batch, i.nextBatchStart, parallelism)
	i.nextBatchStart += batchSize
	return true, err
}

func (c *Client) fetchBlock(block *Block, height int64) error {
	info, err := c.client.Block(c.ctx, &height)
	if err != nil {
		return fmt.Errorf("Block for %d, failed: %w", height, err)
	}

	header := &info.Block.Header
	block.Height = header.Height
	if header.Height != height {
		return fmt.Errorf("Block for %d, wrong height: %d", height, header.Height)
	}

	block.PureBlock = info

	return nil
}

func (c *Client) fetchBlocks(clientIdx int, batch []Block, height int64) error {
	// Note: n > 1 is required
	n := len(batch)
	var err error
	client := c.batchClients[clientIdx]

	last := height + int64(n) - 1
	infos := make([]*coretypes.ResultBlock, n)
	for i := 0; i < n; i++ {
		h := height + int64(i)
		infos[i], err = client.Block(c.ctx, &h)
		if err != nil {
			return fmt.Errorf("Block batch for %d: %w", h, err)
		}
	}
	_, err = client.Send(c.ctx)
	if err != nil {
		return fmt.Errorf("Block batch Send for %d-%d: %w", height, last, err)
	}

	for i, info := range infos {
		h := height + int64(i)

		header := &info.Block.Header
		if header.Height != h {
			return fmt.Errorf("Block for %d, wrong height: %d", h, header.Height)
		}

		block := &batch[i]
		block.Height = header.Height
		block.PureBlock = info
	}

	return nil
}

func (c *Client) fetchBlocksParallel(batch []Block, height int64, parallelism int) error {
	n := len(batch)
	if n == 1 {
		return c.fetchBlock(&batch[0], height)
	}

	if parallelism == 1 {
		return c.fetchBlocks(0, batch, height)
	}

	k := n / parallelism
	if k*parallelism != n {
		return fmt.Errorf("batch size %d not divisible into %d parallel parts", n, parallelism)
	}

	done := make(chan error, parallelism)
	for i := 0; i < parallelism; i++ {
		clientIdx := i
		start := i * k
		go func() {
			err := c.fetchBlocks(clientIdx, batch[start:start+k], height+int64(start))
			done <- err
		}()
	}

	var err error
	for i := 0; i < parallelism; i++ {
		e := <-done
		if e != nil {
			err = e
		}
	}
	return err
}
