package helper

import (
	"errors"
	"fmt"
	"net/http"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if !codebloom.PubliclyAvailable(info.OriginModelName) {
		return types.NewErrorWithStatusCode(errors.New("The selected model is not available"), "model_not_available", http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	// map model name
	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		modelMap := make(map[string]string)
		err := rootcommon.Unmarshal([]byte(modelMapping), &modelMap)
		if err != nil {
			return fmt.Errorf("unmarshal_model_mapping_failed")
		}

		// 支持链式模型重定向，最终使用链尾的模型
		currentModel := info.OriginModelName
		visitedModels := map[string]bool{
			currentModel: true,
		}
		for {
			mappedModel, exists := modelMap[currentModel]
			baseModel := hostreasoning.BaseModelName(currentModel)
			if (!exists || mappedModel == "") && baseModel != currentModel {
				mappedModel, exists = modelMap[baseModel]
			}
			if exists && mappedModel != "" {
				// A reserved pending ID cannot be hidden between an ordinary
				// alias and a final raw supplier ID in a chained mapping.
				_, reviewedDestination := codebloom.Lookup(mappedModel)
				if !codebloom.PubliclyAvailable(mappedModel) || reviewedDestination && mappedModel != info.OriginModelName {
					return types.NewErrorWithStatusCode(errors.New("The selected model is not available"), "model_not_available", http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
				// 模型重定向循环检测，避免无限循环
				if visitedModels[mappedModel] {
					if mappedModel == currentModel {
						if currentModel == info.OriginModelName {
							info.IsModelMapped = false
							return nil
						}

						info.IsModelMapped = true
						break
					}
					return errors.New("model_mapping_contains_cycle")
				}
				visitedModels[mappedModel] = true
				currentModel = mappedModel
				info.IsModelMapped = true
			} else {
				break
			}
		}
		if info.IsModelMapped {
			info.UpstreamModelName = currentModel
		}
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}
