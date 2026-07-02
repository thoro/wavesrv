// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/wavetermdev/waveterm/pkg/util/dbutil"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
)

// can return fs.ErrExist
func dbInsertFile(ctx context.Context, file *WaveFile) error {
	// will fail if file already exists
	return WithTx(ctx, func(tx *TxWrap) error {
		query := "SELECT zoneid FROM db_wave_file WHERE zoneid = ? AND name = ?"
		if tx.Exists(query, file.ZoneId, file.Name) {
			return fs.ErrExist
		}
		query = "INSERT INTO db_wave_file (zoneid, name, size, createdts, modts, opts, meta) VALUES (?, ?, ?, ?, ?, ?, ?)"
		tx.Exec(query, file.ZoneId, file.Name, file.Size, file.CreatedTs, file.ModTs, dbutil.QuickJson(file.Opts), dbutil.QuickJson(file.Meta))
		return nil
	})
}

func dbDeleteFile(ctx context.Context, zoneId string, name string) error {
	return WithTx(ctx, func(tx *TxWrap) error {
		query := "DELETE FROM db_wave_file WHERE zoneid = ? AND name = ?"
		tx.Exec(query, zoneId, name)
		query = "DELETE FROM db_file_data WHERE zoneid = ? AND name = ?"
		tx.Exec(query, zoneId, name)
		return nil
	})
}

func dbGetZoneFileNames(ctx context.Context, zoneId string) ([]string, error) {
	return WithTxRtn(ctx, func(tx *TxWrap) ([]string, error) {
		var files []string
		query := "SELECT name FROM db_wave_file WHERE zoneid = ?"
		tx.Select(&files, query, zoneId)
		return files, nil
	})
}

func dbGetZoneFile(ctx context.Context, zoneId string, name string) (*WaveFile, error) {
	return WithTxRtn(ctx, func(tx *TxWrap) (*WaveFile, error) {
		query := "SELECT * FROM db_wave_file WHERE zoneid = ? AND name = ?"
		file := dbutil.GetMappable[*WaveFile](tx, query, zoneId, name)
		return file, nil
	})
}

func dbGetAllZoneIds(ctx context.Context) ([]string, error) {
	return WithTxRtn(ctx, func(tx *TxWrap) ([]string, error) {
		var ids []string
		query := "SELECT DISTINCT zoneid FROM db_wave_file"
		tx.Select(&ids, query)
		return ids, nil
	})
}

func dbGetFileParts(ctx context.Context, zoneId string, name string, parts []int, compressed bool) (map[int]*DataCacheEntry, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	return WithTxRtn(ctx, func(tx *TxWrap) (map[int]*DataCacheEntry, error) {
		var data []*DataCacheEntry
		query := "SELECT partidx, data FROM db_file_data WHERE zoneid = ? AND name = ? AND partidx IN (SELECT value FROM json_each(?))"
		tx.Select(&data, query, zoneId, name, dbutil.QuickJsonArr(parts))
		rtn := make(map[int]*DataCacheEntry)
		for _, d := range data {
			// decompress BEFORE the partDataSize cap-normalization so the
			// cache holds plaintext bytes capped to the part size (not the
			// shorter compressed blob length)
			if compressed {
				plain, err := decompressPart(d.Data)
				if err != nil {
					return nil, fmt.Errorf("error decompressing part %d for %s:%s: %w", d.PartIdx, zoneId, name, err)
				}
				d.Data = plain
			}
			if cap(d.Data) != int(partDataSize) {
				newData := make([]byte, len(d.Data), partDataSize)
				copy(newData, d.Data)
				d.Data = newData
			}
			rtn[d.PartIdx] = d
		}
		return rtn, nil
	})
}

func dbGetZoneFiles(ctx context.Context, zoneId string) ([]*WaveFile, error) {
	return WithTxRtn(ctx, func(tx *TxWrap) ([]*WaveFile, error) {
		query := "SELECT * FROM db_wave_file WHERE zoneid = ?"
		files := dbutil.SelectMappable[*WaveFile](tx, query, zoneId)
		return files, nil
	})
}

func dbWriteCacheEntry(ctx context.Context, file *WaveFile, dataEntries map[int]*DataCacheEntry, replace bool) error {
	return WithTx(ctx, func(tx *TxWrap) error {
		query := `SELECT zoneid FROM db_wave_file WHERE zoneid = ? AND name = ?`
		if !tx.Exists(query, file.ZoneId, file.Name) {
			// since deletion is synchronous this stops us from writing to a deleted file
			return os.ErrNotExist
		}
		// we don't update CreatedTs or Opts
		query = `UPDATE db_wave_file SET size = ?, modts = ?, meta = ? WHERE zoneid = ? AND name = ?`
		tx.Exec(query, file.Size, file.ModTs, dbutil.QuickJson(file.Meta), file.ZoneId, file.Name)
		if replace {
			query = `DELETE FROM db_file_data WHERE zoneid = ? AND name = ?`
			tx.Exec(query, file.ZoneId, file.Name)
		}
		dataPartQuery := `REPLACE INTO db_file_data (zoneid, name, partidx, data, linecount) VALUES (?, ?, ?, ?, ?)`
		for partIdx, dataEntry := range dataEntries {
			if partIdx != dataEntry.PartIdx {
				panic(fmt.Sprintf("partIdx:%d and dataEntry.PartIdx:%d do not match", partIdx, dataEntry.PartIdx))
			}
			blob := dataEntry.Data
			if file.Opts.Compressed {
				blob = compressPart(dataEntry.Data)
			}
			// the per-part newline count is the line index: cumulative sums over
			// partidx give O(log n) line->byte addressing without decompression
			tx.Exec(dataPartQuery, file.ZoneId, file.Name, dataEntry.PartIdx, blob, bytes.Count(dataEntry.Data, []byte{'\n'}))
		}
		// segmented retention: drop the chunk rows that have fallen out of the
		// retention window so on-disk size stays bounded as the log grows.
		// partidx never wraps, so DataStartIdx/partDataSize is the first seg we keep.
		if file.Opts.Segmented && file.Opts.MaxSize > 0 {
			firstSeg := file.DataStartIdx() / partDataSize
			if firstSeg > 0 {
				delQuery := `DELETE FROM db_file_data WHERE zoneid = ? AND name = ? AND partidx < ?`
				tx.Exec(delQuery, file.ZoneId, file.Name, int(firstSeg))
			}
		}
		return nil
	})
}

func dbUpdateFileOpts(ctx context.Context, zoneId string, name string, opts wshrpc.FileOpts) error {
	return WithTx(ctx, func(tx *TxWrap) error {
		query := `UPDATE db_wave_file SET opts = ? WHERE zoneid = ? AND name = ?`
		tx.Exec(query, dbutil.QuickJson(opts), zoneId, name)
		return nil
	})
}

func dbGetFileDiskUsage(ctx context.Context, zoneId string, name string) (int64, error) {
	return WithTxRtn(ctx, func(tx *TxWrap) (int64, error) {
		query := `SELECT COALESCE(SUM(LENGTH(data)), 0) FROM db_file_data WHERE zoneid = ? AND name = ?`
		return int64(tx.GetInt(query, zoneId, name)), nil
	})
}

type partLineCount struct {
	PartIdx   int
	LineCount int
}

func dbGetPartLineCounts(ctx context.Context, zoneId string, name string) ([]partLineCount, error) {
	return WithTxRtn(ctx, func(tx *TxWrap) ([]partLineCount, error) {
		var counts []partLineCount
		query := `SELECT partidx, linecount FROM db_file_data WHERE zoneid = ? AND name = ? ORDER BY partidx`
		tx.Select(&counts, query, zoneId, name)
		return counts, nil
	})
}

// backfills linecount for legacy part rows (written before the line index existed)
func dbBackfillLineCounts(ctx context.Context, zoneId string, name string, compressed bool) error {
	return WithTx(ctx, func(tx *TxWrap) error {
		var rows []*DataCacheEntry
		query := `SELECT partidx, data FROM db_file_data WHERE zoneid = ? AND name = ? AND linecount < 0`
		tx.Select(&rows, query, zoneId, name)
		updateQuery := `UPDATE db_file_data SET linecount = ? WHERE zoneid = ? AND name = ? AND partidx = ?`
		for _, row := range rows {
			data := row.Data
			if compressed {
				plain, err := decompressPart(data)
				if err != nil {
					return fmt.Errorf("error decompressing part %d for %s:%s: %w", row.PartIdx, zoneId, name, err)
				}
				data = plain
			}
			tx.Exec(updateQuery, bytes.Count(data, []byte{'\n'}), zoneId, name, row.PartIdx)
		}
		return nil
	})
}
