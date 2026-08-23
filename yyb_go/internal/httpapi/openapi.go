package httpapi

func newOpenAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "YYB Go 接口文档",
			"description": "用于微信扫码登录、账号管理和 wxapp 接口调用的 API。",
			"version":     "1.0.0",
		},
		"servers": []map[string]any{
			{"url": "/"},
		},
		"tags": []map[string]any{
			{"name": "health", "description": "服务健康检查"},
			{"name": "auth", "description": "管理员登录鉴权"},
			{"name": "qr", "description": "微信扫码登录"},
			{"name": "accounts", "description": "已保存的微信账号"},
			{"name": "wxapp", "description": "wxapp 业务接口调用"},
		},
		"paths": map[string]any{
			"/health": map[string]any{
				"get": openAPIOperation(
					[]string{"health"},
					"检查服务状态",
					nil,
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("服务正常。", refSchema("HealthResponse")),
					}),
				),
			},
			"/auth/login": map[string]any{
				"post": openAPIOperation(
					[]string{"auth"},
					"管理员登录",
					nil,
					jsonRequestBody(refSchema("LoginRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("登录成功，会话通过 Set-Cookie 下发。", refSchema("LoginResponse")),
					}),
				),
			},
			"/auth/logout": map[string]any{
				"post": openAPIOperation(
					[]string{"auth"},
					"退出登录",
					nil,
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("已退出登录。", objectSchema(nil, map[string]any{
							"logged_out": map[string]any{"type": "boolean"},
						})),
					}),
				),
			},
			"/auth/me": map[string]any{
				"get": openAPIOperation(
					[]string{"auth"},
					"查询当前会话",
					nil,
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("当前登录用户。", refSchema("LoginResponse")),
					}),
				),
			},
			"/auth/password": map[string]any{
				"post": openAPIOperation(
					[]string{"auth"},
					"修改管理员密码",
					nil,
					jsonRequestBody(refSchema("ChangePasswordRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("密码已修改，其他会话已失效。", objectSchema(nil, map[string]any{
							"password_changed": map[string]any{"type": "boolean"},
						})),
					}),
				),
			},
			"/auth/token": map[string]any{
				"post": openAPIOperation(
					[]string{"auth"},
					"生成或轮换 API Token",
					nil,
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("API Token 仅在生成时返回明文。", objectSchema([]string{"token", "token_type"}, map[string]any{
							"token":      map[string]any{"type": "string"},
							"token_type": map[string]any{"type": "string", "example": "Bearer"},
						})),
					}),
				),
				"get": openAPIOperation(
					[]string{"auth"},
					"查询当前 API Token 状态",
					nil,
					nil,
					defaulted(map[string]any{"200": jsonResponse("Token 状态。", freeFormObjectSchema("Token 状态。"))}),
				),
				"delete": openAPIOperation(
					[]string{"auth"},
					"撤销当前 API Token",
					nil,
					nil,
					defaulted(map[string]any{"200": jsonResponse("Token 已撤销。", freeFormObjectSchema("撤销结果。"))}),
				),
			},
			"/qr": map[string]any{
				"post": openAPIOperation(
					[]string{"qr"},
					"创建扫码登录会话",
					[]map[string]any{
						boolQueryParam("as_base64", "是否同时返回二维码图片的 data URI。"),
					},
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("二维码会话创建成功。", refSchema("QRCreateResponse")),
					}),
				),
			},
			"/qr/{session_id}/image": map[string]any{
				"get": openAPIOperation(
					[]string{"qr"},
					"获取二维码图片",
					[]map[string]any{pathStringParam("session_id", "二维码会话 ID。")},
					nil,
					defaulted(map[string]any{
						"200": imageResponse("二维码图片。"),
					}),
				),
			},
			"/qr/{session_id}/poll": map[string]any{
				"get": openAPIOperation(
					[]string{"qr"},
					"轮询扫码登录状态",
					[]map[string]any{pathStringParam("session_id", "二维码会话 ID。")},
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("当前扫码状态。", refSchema("QRPollResponse")),
					}),
				),
			},
			"/qr/{session_id}/confirm": map[string]any{
				"post": openAPIOperation(
					[]string{"qr"},
					"确认已授权的扫码会话并保存账号",
					[]map[string]any{pathStringParam("session_id", "二维码会话 ID。")},
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("已保存的账号信息。", refSchema("AccountPublic")),
					}),
				),
			},
			"/accounts": map[string]any{
				"get": openAPIOperation(
					[]string{"accounts"},
					"获取账号列表",
					nil,
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("已保存的账号列表。", arraySchema(refSchema("AccountPublic"))),
					}),
				),
				"delete": openAPIOperation(
					[]string{"accounts"},
					"删除账号",
					[]map[string]any{queryStringParam("ref", "账号 ID、UIN 或 openid。", true)},
					nil,
					defaulted(map[string]any{
						"200": jsonResponse("删除结果。", refSchema("DeleteAccountResponse")),
					}),
				),
			},
			"/accounts/refresh": map[string]any{
				"post": openAPIOperation(
					[]string{"accounts"},
					"刷新账号存活状态",
					nil,
					jsonOptionalRequestBody(refSchema("AccountRefRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("刷新结果。未传 ref 时返回数组。", refSchema("RefreshResponse")),
					}),
				),
			},
			"/accounts/resync": map[string]any{
				"post": openAPIOperation(
					[]string{"accounts"},
					"重新同步账号资料",
					nil,
					jsonOptionalRequestBody(refSchema("AccountRefRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("同步后的账号信息。未传 ref 时返回数组。", refSchema("ResyncResponse")),
					}),
				),
			},
			"/accounts/avatar": map[string]any{
				"get": openAPIOperation(
					[]string{"accounts"},
					"获取账号头像",
					[]map[string]any{queryStringParam("ref", "账号 ID、UIN 或 openid。", true)},
					nil,
					defaulted(map[string]any{
						"200": imageResponse("头像图片。"),
						"302": map[string]any{"description": "跳转到远程头像地址。"},
					}),
				),
			},
			"/wxapp/getCode": map[string]any{
				"post": openAPIOperation(
					[]string{"wxapp"},
					"获取小程序code",
					nil,
					jsonRequestBody(refSchema("WxappRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("getCode 调用结果。", refSchema("WxappResponse")),
					}),
				),
			},
			"/wxapp/getPhoneNumber": map[string]any{
				"post": openAPIOperation(
					[]string{"wxapp"},
					"获取手机号",
					nil,
					jsonRequestBody(refSchema("WxappRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("getPhoneNumber 调用结果。", refSchema("WxappResponse")),
					}),
				),
			},
			"/wxapp/operateWxData": map[string]any{
				"post": openAPIOperation(
					[]string{"wxapp"},
					"小程序云函数",
					nil,
					jsonRequestBody(refSchema("OperateWXDataRequest")),
					defaulted(map[string]any{
						"200": jsonResponse("operateWxData 调用结果。", refSchema("WxappResponse")),
					}),
				),
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"APIResponse": objectSchema([]string{"code", "msg", "data"}, map[string]any{
					"code": map[string]any{"type": "integer", "example": 0, "description": "业务状态码，0 表示成功，非 0 表示业务错误。"},
					"msg":  map[string]any{"type": "string", "example": "success", "description": "提示信息，前端可直接用于 Toast 提示。"},
					"data": nullableObjectSchema("实际数据载荷，可以是对象、数组或 null。"),
				}),
				"APIErrorResponse": objectSchema([]string{"code", "msg", "data"}, map[string]any{
					"code": map[string]any{"type": "integer", "example": 400, "description": "非 0 业务错误码。"},
					"msg":  map[string]any{"type": "string", "example": "ref is required"},
					"data": nullableObjectSchema("错误响应当前固定返回 null。"),
				}),
				"HealthResponse": objectSchema([]string{"ok"}, map[string]any{
					"ok": map[string]any{"type": "boolean"},
				}),
				"LoginRequest": objectSchema([]string{"username", "password"}, map[string]any{
					"username": map[string]any{"type": "string", "description": "管理员用户名，默认 admin。"},
					"password": map[string]any{"type": "string", "description": "管理员密码。"},
				}),
				"LoginResponse": objectSchema([]string{"username"}, map[string]any{
					"username": map[string]any{"type": "string"},
				}),
				"ChangePasswordRequest": objectSchema([]string{"old_password", "new_password"}, map[string]any{
					"old_password": map[string]any{"type": "string", "description": "当前密码。"},
					"new_password": map[string]any{"type": "string", "description": "新密码，至少 6 位。"},
				}),
				"QRCreateResponse": objectSchema([]string{"session_id", "status", "image_url"}, map[string]any{
					"session_id":   map[string]any{"type": "string"},
					"status":       map[string]any{"type": "string", "example": "pending"},
					"image_url":    map[string]any{"type": "string", "example": "/qr/{session_id}/image"},
					"image_base64": nullableStringSchema("当 as_base64=true 时返回二维码图片 data URI。"),
				}),
				"QRPollResponse": objectSchema([]string{"status"}, map[string]any{
					"status": map[string]any{
						"type": "string",
						"enum": []string{"pending", "scanned", "authorized", "confirmed", "expired", "cancelled", "unknown"},
					},
					"errcode": map[string]any{"type": "integer", "nullable": true},
				}),
				"AccountPublic": objectSchema([]string{"id", "openid", "created_at", "updated_at"}, map[string]any{
					"id":              int64Schema(),
					"openid":          map[string]any{"type": "string"},
					"uin":             nullableInt64Schema(),
					"alias":           nullableStringSchema("账号别名。"),
					"nickname":        nullableStringSchema("账号昵称。"),
					"avatar":          nullableStringSchema("本地头像路径或远程头像 URL。"),
					"status":          nullableStringSchema("账号状态。"),
					"last_checked_at": nullableInt64Schema(),
					"created_at":      int64Schema(),
					"updated_at":      int64Schema(),
				}),
				"RefreshResult": objectSchema([]string{"id", "openid", "status"}, map[string]any{
					"id":       int64Schema(),
					"openid":   map[string]any{"type": "string"},
					"uin":      nullableInt64Schema(),
					"nickname": nullableStringSchema("账号昵称。"),
					"status":   map[string]any{"type": "string", "example": "alive"},
				}),
				"DeleteAccountResponse": objectSchema([]string{"deleted", "openid"}, map[string]any{
					"deleted": int64Schema(),
					"openid":  map[string]any{"type": "string"},
				}),
				"AccountRefRequest": objectSchema(nil, map[string]any{
					"ref": map[string]any{"type": "string", "description": "账号 ID、UIN 或 openid。支持批量操作的接口不传时表示全部账号。"},
				}),
				"RefreshResponse": oneOfSchema(
					refSchema("RefreshResult"),
					arraySchema(refSchema("RefreshResult")),
				),
				"ResyncResponse": oneOfSchema(
					refSchema("AccountPublic"),
					arraySchema(refSchema("AccountPublic")),
				),
				"WxappRequest": objectSchema([]string{"ref", "app_id"}, map[string]any{
					"ref":    map[string]any{"type": "string", "description": "账号 ID、UIN 或 openid。"},
					"app_id": map[string]any{"type": "string"},
				}),
				"OperateWXDataRequest": objectSchema([]string{"ref", "app_id", "payload"}, map[string]any{
					"ref":     map[string]any{"type": "string", "description": "账号 ID、UIN 或 openid。"},
					"app_id":  map[string]any{"type": "string"},
					"payload": freeFormObjectSchema("完整的 operateWxData 请求 JSON。"),
				}),
				"WxappResponse": objectSchema([]string{"openid", "result"}, map[string]any{
					"openid": map[string]any{"type": "string"},
					"result": freeFormObjectSchema("wxapp 接口返回结果。"),
				}),
			},
		},
	}
}

func openAPIOperation(tags []string, summary string, parameters []map[string]any, requestBody map[string]any, responses map[string]any) map[string]any {
	out := map[string]any{
		"tags":      tags,
		"summary":   summary,
		"responses": responses,
	}
	if len(parameters) > 0 {
		out["parameters"] = parameters
	}
	if requestBody != nil {
		out["requestBody"] = requestBody
	}
	return out
}

func defaulted(responses map[string]any) map[string]any {
	responses["default"] = jsonErrorResponse("错误响应。")
	return responses
}

func jsonResponse(description string, schema map[string]any) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": apiResponseSchema(schema),
			},
		},
	}
}

func jsonErrorResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": refSchema("APIErrorResponse"),
			},
		},
	}
}

func imageResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"image/jpeg": map[string]any{
				"schema": map[string]any{"type": "string", "format": "binary"},
			},
		},
	}
}

func jsonRequestBody(schema map[string]any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

func jsonOptionalRequestBody(schema map[string]any) map[string]any {
	return map[string]any{
		"required": false,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

func pathStringParam(name, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"description": description,
		"required":    true,
		"schema":      map[string]any{"type": "string"},
	}
}

func queryStringParam(name, description string, required bool) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"description": description,
		"required":    required,
		"schema":      map[string]any{"type": "string"},
	}
}

func boolQueryParam(name, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"description": description,
		"required":    false,
		"schema":      map[string]any{"type": "boolean"},
	}
}

func oneOfSchema(schemas ...map[string]any) map[string]any {
	return map[string]any{"oneOf": schemas}
}

func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func arraySchema(item map[string]any) map[string]any {
	return map[string]any{
		"type":  "array",
		"items": item,
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func apiResponseSchema(dataSchema map[string]any) map[string]any {
	if dataSchema == nil {
		dataSchema = nullableObjectSchema("实际数据载荷。")
	}
	return objectSchema([]string{"code", "msg", "data"}, map[string]any{
		"code": map[string]any{"type": "integer", "example": 0, "description": "业务状态码，0 表示成功，非 0 表示业务错误。"},
		"msg":  map[string]any{"type": "string", "example": "success", "description": "提示信息，前端可直接用于 Toast 提示。"},
		"data": dataSchema,
	})
}

func freeFormObjectSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": true,
		"nullable":             true,
	}
}

func nullableObjectSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": true,
		"nullable":             true,
	}
}

func nullableStringSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"nullable":    true,
	}
}

func int64Schema() map[string]any {
	return map[string]any{"type": "integer", "format": "int64"}
}

func nullableInt64Schema() map[string]any {
	return map[string]any{"type": "integer", "format": "int64", "nullable": true}
}
