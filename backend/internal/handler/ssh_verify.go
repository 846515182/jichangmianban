package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	"nexus-panel/internal/app"
)

// trustOnFirstUse SSH 主机密钥验证（TOFU 策略）
// prefix: Redis 键前缀，用于区分不同场景（终端/部署）
func trustOnFirstUse(prefix string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		redisKey := prefix + hostname

		rdb := app.Get().RDB
		if rdb == nil {
			log.Printf("[SSH-TOFU] Redis 不可用，信任首次连接 %s (%s)", hostname, fingerprint)
			return nil
		}

		stored, err := rdb.Get(context.Background(), redisKey).Result()
		if err != nil {
			// 首次连接：存储指纹
			encoded := base64.StdEncoding.EncodeToString(key.Marshal())
			if setErr := rdb.Set(context.Background(), redisKey, encoded, 0).Err(); setErr != nil {
				log.Printf("[SSH-TOFU] 存储主机密钥失败 %s: %v", hostname, setErr)
			}
			log.Printf("[SSH-TOFU] 首次信任主机 %s，指纹: %s", hostname, fingerprint)
			return nil
		}

		// 后续连接：验证指纹是否一致
		expectedKey, err := base64.StdEncoding.DecodeString(stored)
		if err != nil {
			log.Printf("[SSH-TOFU] 存储的主机密钥解码失败 %s: %v", hostname, err)
			return fmt.Errorf("主机密钥验证失败：存储数据损坏")
		}

		expectedPubKey, err := ssh.ParsePublicKey(expectedKey)
		if err != nil {
			log.Printf("[SSH-TOFU] 解析存储的主机密钥失败 %s: %v", hostname, err)
			return fmt.Errorf("主机密钥验证失败：存储数据损坏")
		}

		actualFP := ssh.FingerprintSHA256(key)
		expectedFP := ssh.FingerprintSHA256(expectedPubKey)
		if !bytes.Equal(expectedPubKey.Marshal(), key.Marshal()) {
			log.Printf("[SSH-TOFU] ⚠️ 主机密钥变更 %s！期望: %s，实际: %s",
				hostname, expectedFP, actualFP)
			return fmt.Errorf(
				"主机密钥验证失败！密钥指纹与首次连接时不一致，可能存在中间人攻击。\n期望: %s\n实际: %s\n如需重新信任，请联系管理员清除旧指纹。",
				expectedFP, actualFP)
		}

		return nil
	}
}

// [FIX 2026-07-21] 删除遗留的 FingerprintSHA256Equal 死代码:
//   该函数定义后从未被任何地方引用(全仓 grep 仅命中此文件), 且实现只是 a == b
//   没有任何抽象价值, 直接删除避免误导.


// [P0#1 2026-07-14] loadStrictHostKey 严格 known_hosts 模式:
// 拒绝所有未在文件中预先登记的主机。
// 文件不存在 → 拒绝连接(安全优先)
// 文件存在   → 严格校验指纹,不一致则拒绝
func loadStrictHostKey(path string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[SSH-STRICT] known_hosts 文件 %s 读取失败: %v", path, err)
			return fmt.Errorf("主机未在 known_hosts 中授权: %s", hostname)
		}

		fingerprint := ssh.FingerprintSHA256(key)
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !strings.Contains(line, hostname) {
				continue
			}
			stored, _, _, _, parseErr := ssh.ParseAuthorizedKey([]byte(line))
			if parseErr != nil {
				continue
			}
			if bytes.Equal(stored.Marshal(), key.Marshal()) {
				log.Printf("[SSH-STRICT] 主机 %s 验证通过 (%s)", hostname, fingerprint)
				return nil
			}
		}

		log.Printf("[SSH-STRICT] ⚠️ 主机 %s 未授权或密钥不匹配 (指纹: %s)", hostname, fingerprint)
		return fmt.Errorf("主机未在 known_hosts 中授权: %s", hostname)
	}
}
