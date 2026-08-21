package speechbench

import (
	"strings"
	"unicode"
)

func NormalizeText(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var out []rune
	space := false
	for _, r := range []rune(input) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r); space = false; continue
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			if len(out)>0 && !space { out=append(out,' '); space=true }
		}
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

func WER(reference, hypothesis string) float64 {
	ref := strings.Fields(NormalizeText(reference)); hyp := strings.Fields(NormalizeText(hypothesis))
	if len(ref)==0 { if len(hyp)==0{return 0}; return 1 }
	return float64(editStrings(ref,hyp))/float64(len(ref))
}

func CER(reference, hypothesis string) float64 {
	ref := compactRunes(NormalizeText(reference)); hyp := compactRunes(NormalizeText(hypothesis))
	if len(ref)==0 { if len(hyp)==0{return 0}; return 1 }
	return float64(editRunes(ref,hyp))/float64(len(ref))
}

func compactRunes(s string) []rune { var out []rune; for _,r:=range []rune(s){if !unicode.IsSpace(r){out=append(out,r)}}; return out }
func editStrings(a,b []string) int { prev:=make([]int,len(b)+1); for j:=range prev{prev[j]=j}; for i:=1;i<=len(a);i++{cur:=make([]int,len(b)+1);cur[0]=i;for j:=1;j<=len(b);j++{cost:=0;if a[i-1]!=b[j-1]{cost=1};cur[j]=min3(cur[j-1]+1,prev[j]+1,prev[j-1]+cost)};prev=cur};return prev[len(b)] }
func editRunes(a,b []rune) int { prev:=make([]int,len(b)+1); for j:=range prev{prev[j]=j}; for i:=1;i<=len(a);i++{cur:=make([]int,len(b)+1);cur[0]=i;for j:=1;j<=len(b);j++{cost:=0;if a[i-1]!=b[j-1]{cost=1};cur[j]=min3(cur[j-1]+1,prev[j]+1,prev[j-1]+cost)};prev=cur};return prev[len(b)] }
func min3(a,b,c int) int { if b<a{a=b}; if c<a{a=c}; return a }
