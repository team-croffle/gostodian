// Package judge는 모델이 보고서에 적는 판정 단어다.
// 수집 본문을 규칙으로 세지 않는다.
package judge

const (
	// ResultClear는 수집은 됐으나 모델이 이상을 찾지 않았을 때다.
	ResultClear = "이상 없음"
	// ResultFound는 모델이 이상을 찾았을 때다.
	ResultFound = "이상 있음"
	// ResultMissing은 그 종류 수집이 실패해 판정할 본문이 없을 때다.
	ResultMissing = "수집 없음"
	// ResultPending은 수집은 됐으나 모델이 판정을 주지 않았을 때다.
	ResultPending = "판정 없음"
)

// Result는 종류 하나의 판정이다.
type Result struct {
	Category string
	Result   string
	Matches  int
}
