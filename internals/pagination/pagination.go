package pagination

import (
	"MTL_Scheduler_PII_Test/internals/models"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultLimit     = 50
	maxLimit         = 200
	PaginationOffset = "offset"
	PaginationCursor = "cursor"
)

type Params struct {
	Limit  int
	Offset int
	Cursor string
	Mode   string
}

type PageInfo struct {
	Limit      int    `json:"limit"`
	Count      int    `json:"count"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	Offset     *int   `json:"offset,omitempty"`
	Total      *int64 `json:"total,omitempty"`
}

func EncodeCursor(t time.Time, id uint) string {
	raw := t.Format(time.RFC3339Nano) + "|" + strconv.FormatUint(uint64(id), 10)
	result := base64.StdEncoding.EncodeToString([]byte(raw))
	return result
}

func DecodeCursor(s string) (time.Time, uint, error) {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, err
	}

	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("invalid cursor format")
	}

	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, err
	}

	id64, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, err
	}

	return t, uint(id64), nil
}

func ParseParams(c *gin.Context) (Params, error) {
	var p Params

	p.Cursor = c.Query("cursor")
	p.Mode = PaginationCursor

	offsetRaw := c.Query("offset")
	limitRaw := c.Query("limit")

	if p.Cursor != "" && offsetRaw != "" {
		return Params{}, fmt.Errorf("cursor and offset are mutually exclusive")
	}

	if offsetRaw != "" {
		p.Mode = PaginationOffset

		offset, err := strconv.Atoi(offsetRaw)
		if err != nil || offset < 0 {
			return Params{}, fmt.Errorf("invalid offset: %s", offsetRaw)
		}

		p.Offset = offset
	}

	p.Limit = defaultLimit

	if limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil || limit <= 0 {
			return Params{}, fmt.Errorf("invalid limit: %s", limitRaw)
		}

		p.Limit = limit

		if p.Limit > maxLimit {
			p.Limit = maxLimit
		}
	}

	return p, nil
}

func ApplyOffset(db *gorm.DB, p Params) *gorm.DB {
	return db.Offset(p.Offset).Limit(p.Limit)
}

func ApplyCursor(db *gorm.DB, p Params, tsColumn string) (*gorm.DB, error) {
	q := db.Order(tsColumn + " DESC, id DESC")

	if p.Cursor == "" {
		return q.Limit(p.Limit), nil
	}

	cursorTS, cursorID, err := DecodeCursor(p.Cursor)
	if err != nil {
		return nil, err
	}

	q = q.Where(
		tsColumn+" < ? OR ("+tsColumn+" = ? AND id < ?)",
		cursorTS, cursorTS, cursorID,
	)

	return q.Limit(p.Limit), nil
}

func BuildPageInfo(p Params, rowCount int, lastTS time.Time, lastID uint, total *int64) PageInfo {
	info := PageInfo{
		Limit:   p.Limit,
		Count:   rowCount,
		HasMore: rowCount == p.Limit,
	}

	if p.Mode == PaginationCursor {
		if info.HasMore && rowCount > 0 {
			info.NextCursor = EncodeCursor(lastTS, lastID)
		}
	} else {
		info.Offset = &p.Offset
		info.Total = total
	}

	return info
}

func ApplyPagination(query *gorm.DB, p Params, tsColumn string) (*gorm.DB, *int64, error) {
	var total *int64

	if p.Mode == PaginationOffset {
		var count int64
		if err := query.Model(&models.Task{}).Count(&count).Error; err != nil {
			return nil, nil, err
		}

		total = &count
		query = query.Order(tsColumn + " DESC, id DESC")
	}

	switch p.Mode {
	case PaginationCursor:
		var err error
		query, err = ApplyCursor(query, p, tsColumn)
		if err != nil {
			return nil, nil, err
		}
	case PaginationOffset:
		query = ApplyOffset(query, p)
	}

	return query, total, nil
}
