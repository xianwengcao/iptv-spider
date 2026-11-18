package model

import "testing"

func TestJsonResponseErrInfo(t *testing.T) {
	cases := []struct {
		name string
		resp JsonResponse[ChannelInfo]
		want string // 空字符串表示判定为正常
	}{
		// 关键回归用例：getChannelList 正常返回 169 条数据时 errCode/errMsg
		// 为空，必须判定为正常（曾在响应外壳里按 status 判断，把这种
		// 正常返回误报成接口错误）。
		{"有数据且 errCode 为空视为正常", JsonResponse[ChannelInfo]{
			Data: []ChannelInfo{{MixNo: "1"}},
		}, ""},
		{"errCode/errMsg 均为空", JsonResponse[ChannelInfo]{}, ""},
		{"errCode=0 视为正常", JsonResponse[ChannelInfo]{ErrCode: "0"}, ""},
		{"errCode 含空格视为正常", JsonResponse[ChannelInfo]{ErrCode: "  "}, ""},
		{"errCode 非 0 视为错误", JsonResponse[ChannelInfo]{
			ErrCode: "1001", ErrMsg: "参数错误",
		}, "errCode=1001 errMsg=参数错误"},
		{"051002 同样属于错误信息", JsonResponse[ChannelInfo]{
			ErrCode: "051002", ErrMsg: "根据频道ID[x], 未获取到节目单数据",
		}, "errCode=051002 errMsg=根据频道ID[x], 未获取到节目单数据"},
	}
	for _, c := range cases {
		if got := c.resp.ErrInfo(); got != c.want {
			t.Errorf("%s: 期望 %q，实际 %q", c.name, c.want, got)
		}
	}
}

func TestJsonResponseIsNoData(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"051002", true},
		{" 051002 ", true},
		{"1001", false},
		{"0", false},
		{"", false},
	}
	for _, c := range cases {
		r := JsonResponse[ChannelInfo]{ErrCode: c.code}
		if got := r.IsNoData(); got != c.want {
			t.Errorf("errCode=%q: 期望 %v，实际 %v", c.code, c.want, got)
		}
	}
}
