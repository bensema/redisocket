package redisocket

type Auth struct {
	UserId   string            `json:"user_id"`
	UserInfo map[string]string `json:"user_info"`
}
