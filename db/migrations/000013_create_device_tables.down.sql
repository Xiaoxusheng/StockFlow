-- 000013 设备与扫码表回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS uk_app_versions_platform_code;
DROP TABLE IF EXISTS app_versions;

DROP INDEX IF EXISTS idx_device_logs_level_occurred;
DROP INDEX IF EXISTS idx_device_logs_device_occurred;
DROP TABLE IF EXISTS device_logs;

DROP INDEX IF EXISTS idx_scan_logs_raw_code;
DROP INDEX IF EXISTS idx_scan_logs_created_at;
DROP INDEX IF EXISTS idx_scan_logs_user_created;
DROP INDEX IF EXISTS idx_scan_logs_device_created;
DROP TABLE IF EXISTS scan_logs;

DROP INDEX IF EXISTS idx_device_configs_version;
DROP INDEX IF EXISTS uk_device_configs_device;
DROP TABLE IF EXISTS device_configs;

DROP INDEX IF EXISTS idx_devices_last_online;
DROP INDEX IF EXISTS idx_devices_warehouse;
DROP INDEX IF EXISTS idx_devices_type_status;
DROP INDEX IF EXISTS uk_devices_code;
DROP TABLE IF EXISTS devices;
