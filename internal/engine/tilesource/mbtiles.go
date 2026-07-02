package tilesource

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo), registered as "sqlite"
)

// MBTiles reads tiles from an .mbtiles SQLite archive (the OGC/MapBox spec): a
// `tiles(zoom_level, tile_column, tile_row, tile_data)` table plus a `metadata`
// key/value table. mbtiles addresses rows in TMS (y flipped vs XYZ), which Tile
// converts. Stored vector tiles are conventionally gzipped; Tile gunzips them so
// callers always receive raw MVT.
type MBTiles struct {
	db   *sql.DB
	meta TileMeta
}

// OpenMBTiles opens an .mbtiles archive read-only and loads its metadata. Close
// releases the database handle.
func OpenMBTiles(path string) (*MBTiles, error) {
	// Read-only + immutable: we never write, and immutable skips lock/WAL probing
	// so a bare archive on read-only media opens cleanly.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	m := &MBTiles{db: db}
	if err := m.loadMeta(); err != nil {
		db.Close()
		return nil, err
	}
	return m, nil
}

// loadMeta reads the metadata table into m.meta. Missing keys leave zero values;
// a malformed value is ignored rather than failing the open.
func (m *MBTiles) loadMeta() error {
	rows, err := m.db.Query("SELECT name, value FROM metadata")
	if err != nil {
		return fmt.Errorf("mbtiles: read metadata: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return err
		}
		switch strings.ToLower(name) {
		case "minzoom":
			if v, e := strconv.ParseUint(value, 10, 8); e == nil {
				m.meta.MinZoom = uint8(v)
			}
		case "maxzoom":
			if v, e := strconv.ParseUint(value, 10, 8); e == nil {
				m.meta.MaxZoom = uint8(v)
			}
		case "bounds": // "west,south,east,north" in degrees
			parseBounds(value, &m.meta)
		case "compression":
			m.meta.Gzipped = strings.EqualFold(value, "gzip")
		case "format":
			if strings.EqualFold(value, "pbf") {
				m.meta.Gzipped = true // pbf in mbtiles is gzipped by convention
			}
			if strings.EqualFold(value, "mlt") {
				m.meta.TileType = "mlt" // MLT tiles: hint the client decoder
			}
		}
	}
	if m.meta.TileType == "" {
		m.meta.TileType = "mvt" // mbtiles archives here are legacy Go-baked MVT
	}
	return rows.Err()
}

// parseBounds fills meta's W/S/E/N from a "west,south,east,north" metadata value.
func parseBounds(value string, meta *TileMeta) {
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return
	}
	dst := []*float64{&meta.W, &meta.S, &meta.E, &meta.N}
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return
		}
		*dst[i] = v
	}
}

// Meta returns the archive's metadata.
func (m *MBTiles) Meta() TileMeta { return m.meta }

// Close releases the database handle.
func (m *MBTiles) Close() error { return m.db.Close() }

// Tile returns the decompressed MVT for z/x/y (XYZ), or nil if the archive holds
// no tile there. The XYZ y is flipped to mbtiles' TMS row.
func (m *MBTiles) Tile(z uint8, x, y uint32) ([]byte, error) {
	tmsRow := (uint32(1) << z) - 1 - y
	var body []byte
	err := m.db.QueryRow(
		"SELECT tile_data FROM tiles WHERE zoom_level=? AND tile_column=? AND tile_row=?",
		z, x, tmsRow,
	).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if isGzip(body) {
		return gunzipBytes(body)
	}
	return body, nil
}

// isGzip reports whether b begins with the gzip magic.
func isGzip(b []byte) bool { return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b }

func gunzipBytes(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
