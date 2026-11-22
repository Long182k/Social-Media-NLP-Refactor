package lexicon

// Sentiment represents the 3-label classification used across the platform.
type Sentiment string

const (
	SentimentGood     Sentiment = "GOOD"
	SentimentModerate Sentiment = "MODERATE"
	SentimentBad      Sentiment = "BAD"
)

var emojiWeights = map[rune]float64{
	'❤': 1.0,
	'😍': 1.2,
	'🥰': 1.2,
	'😘': 1.0,
	'🔥': 1.0,
	'👍': 1.0,
	'🎉': 1.0,
	'🥳': 1.1,
	'😊': 0.9,
	'😁': 0.9,
	'😄': 0.9,
	'💯': 1.1,
	'✨': 0.7,
	'🌟': 0.8,
	'👏': 0.9,
	'💚': 0.9,
	'💙': 0.9,
	'😡': -1.2,
	'🤬': -1.3,
	'😢': -1.0,
	'😭': -1.1,
	'👎': -1.0,
	'💔': -1.1,
	'🤮': -1.3,
	'🤢': -1.1,
	'😞': -1.0,
	'😔': -0.9,
	'😤': -0.9,
	'💩': -1.2,
	'😒': -0.8,
}

var negators = map[string]struct{}{
	// Vietnamese
	"không": {}, "ko": {}, "k": {}, "khong": {},
	"chẳng": {}, "chả": {}, "chưa": {}, "đừng": {},
	"đéo": {}, "hem": {}, "hông": {},
	"không hề": {}, "không phải": {}, "đâu có": {}, "chẳng hề": {},
	// English
	"not": {}, "never": {}, "no": {}, "none": {}, "neither": {},
	"hardly": {}, "barely": {}, "without": {},
	"don't": {}, "doesn't": {}, "didn't": {}, "isn't": {}, "wasn't": {},
	"aren't": {}, "weren't": {}, "won't": {}, "wouldn't": {}, "can't": {},
	"couldn't": {}, "shouldn't": {}, "haven't": {}, "hasn't": {}, "ain't": {},
}

var intensifiers = map[string]float64{
	// Vietnamese
	"rất": 1.5, "quá": 1.5, "cực": 1.7, "cực kỳ": 1.8, "cực kì": 1.8,
	"vô cùng": 1.8, "siêu": 1.6, "khá": 1.2, "hơi": 0.6, "hết sức": 1.7,
	"quá trời": 1.6, "thật sự": 1.4, "thực sự": 1.4, "vô đối": 1.7,
	// English
	"very": 1.5, "extremely": 1.8, "super": 1.6, "really": 1.4, "so": 1.4,
	"totally": 1.5, "incredibly": 1.7, "absolutely": 1.7, "quite": 1.2, "slightly": 0.6,
}

var threeGrams = map[string]float64{
	"tuyệt cú mèo":   1.8,
	"cực kỳ tốt":     1.9,
	"cực kỳ hay":     1.9,
	"cực kỳ tệ":      -1.9,
	"không thể tin":  0.3,
	"đáng đồng tiền": 1.5,
	"phí thời gian":  -1.6,
	"waste of time":  -1.6,
	"out of control": -1.0,
}

var twoGrams = map[string]float64{
	// Vietnamese positive
	"tuyệt vời": 1.7, "xuất sắc": 1.8, "xịn xò": 1.5, "đỉnh cao": 1.6,
	"dễ thương": 1.3, "hài lòng": 1.4, "hạnh phúc": 1.5, "vui vẻ": 1.3,
	"tuyệt hảo": 1.7, "ưng ý": 1.4, "ổn áp": 1.2, "ngon lành": 1.4,
	"mượt mà": 1.3, "nhiệt tình": 1.2, "tốt đẹp": 1.4, "thích thú": 1.3,
	"hoàn hảo": 1.8, "ấn tượng": 1.4, "đáng yêu": 1.4, "tuyệt đẹp": 1.6,
	"quá đỉnh": 1.8, "quá đã": 1.6, "hết sẩy": 1.6,
	// Unaccented unambiguous VI positive
	"tuyet voi": 1.6, "xuat sac": 1.7, "de thuong": 1.2, "hai long": 1.3,
	"on ap": 1.1, "xin xo": 1.4,
	// Vietnamese negative
	"quá tệ": -1.8, "chán nản": -1.4, "bực mình": -1.4, "cùi bắp": -1.5,
	"thất vọng": -1.6, "tồi tệ": -1.7, "khó chịu": -1.3, "mệt mỏi": -1.2,
	"kinh khủng": -1.7, "tệ hại": -1.7, "vô dụng": -1.6, "rác rưởi": -1.8,
	"lừa đảo": -1.9, "chậm chạp": -1.2, "nóng nảy": -1.1, "buồn bã": -1.3,
	"tức giận": -1.5, "chán ngắt": -1.5, "quá dở": -1.6,
	// Unaccented unambiguous VI negative
	"qua te": -1.7, "chan nan": -1.3, "that vong": -1.5, "toi te": -1.6,
	"cui bap": -1.4,
	// Neutral multi-word
	"bình thường": 0.0, "tạm ổn": 0.25, "tạm được": 0.2,
	// English multi-word
	"well done": 1.5, "spot on": 1.4, "love it": 1.5, "super fast": 1.4,
	"look forward": 0.8, "broken down": -1.4, "fell apart": -1.5, "no good": -1.4,
}

var unigrams = map[string]float64{
	// Vietnamese positive
	"tốt": 1.2, "hay": 1.2, "đẹp": 1.2, "vui": 1.2, "thích": 1.1,
	"yêu": 1.3, "tuyệt": 1.5, "ngon": 1.2, "xịn": 1.4, "đỉnh": 1.5,
	"mlem": 1.2, "ưng": 1.2, "chất": 1.2, "mượt": 1.2, "okela": 1.0,
	"sướng": 1.3, "tuyệtvời": 1.6, "thơm": 1.0, "khỏe": 1.0, "giỏi": 1.2,
	"nhanh": 0.8, "rẻ": 0.7, "xinh": 1.2, "thắng": 1.1, "cảm ơn": 1.0,
	"tuyet": 1.3, "xin": 1.2,
	// Vietnamese negative
	"tệ": -1.5, "xấu": -1.3, "chán": -1.3, "buồn": -1.2, "ghét": -1.5,
	"dở": -1.3, "bực": -1.3, "tức": -1.3, "phèn": -1.2, "cùi": -1.4,
	"rác": -1.6, "toang": -1.5, "ngán": -1.2, "lag": -1.1, "giật": -0.9,
	"lỗi": -1.2, "hỏng": -1.3, "kém": -1.3, "thua": -1.1, "sợ": -0.9,
	"đau": -1.1, "khóc": -1.1, "nhạt": -0.9, "ế": -0.8, "ngu": -1.5,
	// English positive
	"good": 1.2, "great": 1.5, "awesome": 1.6, "amazing": 1.7, "excellent": 1.7,
	"wonderful": 1.6, "fantastic": 1.7, "love": 1.4, "loved": 1.4, "loving": 1.3,
	"like": 0.9, "liked": 0.9, "nice": 1.1, "happy": 1.3, "glad": 1.1,
	"enjoy": 1.2, "enjoyed": 1.2, "best": 1.5, "brilliant": 1.6, "clean": 0.9,
	"fast": 0.9, "smooth": 1.1, "helpful": 1.1, "cool": 1.0, "perfect": 1.7,
	"lit": 1.3, "goat": 1.5, "fire": 1.2, "solid": 1.0, "impressive": 1.4,
	"congrats": 1.3, "thanks": 0.9, "thank": 0.8, "win": 1.1, "yay": 1.2,
	// English negative
	"bad": -1.3, "terrible": -1.7, "awful": -1.7, "horrible": -1.7, "worst": -1.8,
	"hate": -1.5, "hated": -1.5, "dislike": -1.2, "sad": -1.2, "angry": -1.4,
	"upset": -1.3, "annoying": -1.3, "annoyed": -1.3, "disappointed": -1.5,
	"disappointing": -1.5, "poor": -1.2, "slow": -0.9, "broken": -1.4,
	"trash": -1.6, "garbage": -1.6, "cringe": -1.3, "sucks": -1.5, "suck": -1.5,
	"buggy": -1.3, "bug": -0.9, "crash": -1.3, "crashed": -1.4, "fail": -1.3,
	"failed": -1.3, "useless": -1.5, "boring": -1.2, "ugly": -1.3, "painful": -1.3,
	"toxic": -1.5, "pathetic": -1.6, "scam": -1.8, "mid": -0.6,
}
