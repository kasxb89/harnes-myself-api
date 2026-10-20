package controller

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetBalanceLedger(c *gin.Context) {
	filter := model.BalanceLedgerFilter{Category: c.DefaultQuery("category", "all"), Username: strings.TrimSpace(c.Query("username"))}
	page, pageSize := 1, 20
	valid := filter.Category == "all" || slices.Contains(model.BalanceLedgerCategories, filter.Category)
	for key, target := range map[string]*int{"p": &page, "page_size": &pageSize, "user_id": &filter.UserID} {
		if value, present := c.GetQuery(key); present {
			number, err := strconv.Atoi(value)
			if err != nil || number < 1 {
				valid = false
			} else {
				*target = number
			}
		}
	}
	for key, target := range map[string]*int64{"start_timestamp": &filter.Start, "end_timestamp": &filter.End} {
		if value, present := c.GetQuery(key); present {
			number, err := strconv.ParseInt(value, 10, 64)
			if err != nil || number < 0 || number > 253402300799 {
				valid = false
			} else {
				*target = number
			}
		}
	}
	if !valid || len([]rune(filter.Username)) > 64 || pageSize > 100 || page > 100000/pageSize || (filter.Start > 0 && filter.End > 0 && filter.Start > filter.End) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	items, total, summary, err := model.GetBalanceLedger(filter, (page-1)*pageSize, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total, "p": page, "page_size": pageSize, "summary": summary})
}
