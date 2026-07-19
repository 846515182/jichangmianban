-- 修复历史数据: 已软删用户的 email 仍占唯一索引, 加 _del_ 后缀释放
-- 不动 username(代码层之前已处理), 只修 email
-- 加 WHERE 条件避免重复修改: 只改 is_deleted=true 且 email 不含 '_del_' 的
UPDATE users
SET email = email || '_del_' || to_char(now(), 'YYYYMMDDHH24MISS')
WHERE is_deleted = true
  AND email NOT LIKE '%_del_%';
