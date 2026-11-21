package api

import "testing"

// TestNormalizeDaysAgo 锁定 daysAgo 档位归一化行为：
// 任意请求值向上取整到 {1, 3, 7} 档，保证缓存最多 3 份。
func TestNormalizeDaysAgo(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{-100, 1},
		{-1, 1},
		{0, 1},
		{1, 1},
		{2, 3},  // 2 向上靠到 3：给超集而不是缺数据
		{3, 3},
		{4, 7},  // 4~6 向上靠到 7
		{5, 7},
		{6, 7},
		{7, 7},
		{8, 7},  // 超出上限一律封顶 7
		{9, 7},
		{999, 7},
	}
	for _, c := range cases {
		if got := normalizeDaysAgo(c.in); got != c.want {
			t.Errorf("normalizeDaysAgo(%d) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}
