package lexicon

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestScoreVietnameseAndEnglish(t *testing.T) {
	cases := []struct {
		name string
		text string
		want Sentiment
	}{
		{name: "VI positive diacritics", text: "Hôm nay sản phẩm này thật tuyệt vời và xịn xò!", want: SentimentGood},
		{name: "VI negative diacritics", text: "Dịch vụ quá tệ, thực sự thất vọng và bực mình", want: SentimentBad},
		{name: "VI negation flips positive", text: "Món này không ngon chút nào, chẳng tốt", want: SentimentBad},
		{name: "VI negation flips negative", text: "Tưởng dở mà không tệ chút nào, khá ổn áp", want: SentimentGood},
		{name: "VI intensifier + slang", text: "Giao diện mượt vãi, cực kỳ đỉnh cao 🔥", want: SentimentGood},
		{name: "VI negative slang", text: "App bị toang rồi, code cùi bắp quá", want: SentimentBad},
		{name: "VI neutral", text: "Hôm nay mình đi họp lúc 9 giờ sáng tại văn phòng.", want: SentimentModerate},
		{name: "EN positive", text: "This refactor is super fast, clean, and awesome!", want: SentimentGood},
		{name: "EN negative", text: "Terrible release, buggy and completely broken.", want: SentimentBad},
		{name: "EN negation", text: "I don't like this update, not good at all", want: SentimentBad},
		{name: "EN neutral", text: "Meeting scheduled for tomorrow afternoon at 2 PM.", want: SentimentModerate},
		{name: "Emoji positive", text: "❤️🎉🔥", want: SentimentGood},
		{name: "Emoji negative", text: "😡👎💔", want: SentimentBad},
		{name: "Empty", text: "   ", want: SentimentModerate},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(tc.text)
			if got != tc.want {
				_, raw := ScoreDetail(tc.text)
				t.Fatalf("Score(%q) = %s (raw=%.2f), want %s", tc.text, got, raw, tc.want)
			}
		})
	}
}

func TestScoreNFDMatchesNFC(t *testing.T) {
	nfc := "Hôm nay dịch vụ rất tuyệt vời và không hề tệ!"
	nfd := norm.NFD.String(nfc)
	if nfc == nfd {
		t.Fatal("expected NFD string to differ from NFC")
	}
	s1, v1 := ScoreDetail(nfc)
	s2, v2 := ScoreDetail(nfd)
	if s1 != s2 || v1 != v2 {
		t.Fatalf("NFC (%s %.2f) != NFD (%s %.2f)", s1, v1, s2, v2)
	}
}

func BenchmarkScoreVI(b *testing.B) {
	sample := "Hôm nay ứng dụng chạy cực kỳ mượt mà và giao diện xịn xò quá trời, nhưng hôm qua hơi lag nhẹ chút thôi ❤️"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Score(sample)
	}
}

func BenchmarkScoreEN(b *testing.B) {
	sample := "The new Go backend is super fast, really clean, and not buggy at all! Love this awesome upgrade 🔥"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Score(sample)
	}
}
