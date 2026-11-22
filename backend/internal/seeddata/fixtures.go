package seeddata

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/lexicon"
)

type SeedUser struct {
	ID       uuid.UUID
	UserName string
	NickName string
	Email    string
	Role     string
	IsActive bool
}

type SeedPost struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Content   string
	Sentiment string
	CreatedAt time.Time
}

var postTemplates = []string{
	"Hôm nay sản phẩm mới chạy rất mượt mà và giao diện thật sự tuyệt vời! ❤️",
	"Dịch vụ chăm sóc khách hàng quá tệ, thực sự thất vọng và bực mình 😡",
	"Mình vừa họp buổi sáng tại văn phòng để chốt kế hoạch tuần này.",
	"The new Go backend is super fast, clean, and awesome! 🔥",
	"This release feels buggy and broken, really disappointed with the crash.",
	"Updated the documentation and scheduled the weekly sync for Thursday afternoon.",
	"Giao diện tối ưu xịn xò quá trời, trải nghiệm cực kỳ đỉnh cao 🎉",
	"App bị toang rồi, tải mãi không xong, cùi bắp thật sự 👎",
	"Hôm nay thời tiết bình thường, mình ghé quán cà phê làm việc.",
	"Món này không ngon như kỳ vọng, phục vụ cũng hơi chậm.",
	"Tưởng dở mà không tệ chút nào, khá ổn áp và dễ dùng!",
}

func DeterministicUsers() []SeedUser {
	users := make([]SeedUser, 0, 105)

	add := func(userName, nickName, email, role string) {
		users = append(users, SeedUser{
			ID:       uuid.NewSHA1(uuid.NameSpaceURL, []byte("smn-seed-user:"+email)),
			UserName: userName,
			NickName: nickName,
			Email:    email,
			Role:     role,
			IsActive: true,
		})
	}

	add("alice", "Alice Johnson", "alice@example.com", "USER")
	add("bob", "Bob Builder", "bob@example.com", "USER")
	for i := 1; i <= 103; i++ {
		role := "USER"
		if i%10 == 0 {
			role = "ADMIN"
		}
		add(
			fmt.Sprintf("user%d", i),
			fmt.Sprintf("User %d", i),
			fmt.Sprintf("user%d@example.com", i),
			role,
		)
	}
	return users
}

func DeterministicPosts(users []SeedUser) []SeedPost {
	baseTime := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	posts := make([]SeedPost, 0, 110)

	for i := 0; i < 110; i++ {
		author := users[i%len(users)]
		tmpl := postTemplates[i%len(postTemplates)]
		content := fmt.Sprintf("[#%03d] %s", i+1, tmpl)
		sentiment := string(lexicon.Score(content))
		posts = append(posts, SeedPost{
			ID:        uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("smn-seed-post:%d", i+1))),
			UserID:    author.ID,
			Content:   content,
			Sentiment: sentiment,
			CreatedAt: baseTime.Add(-time.Duration(i) * time.Minute),
		})
	}
	return posts
}
