import {ApiError} from '@ct/shared'

const messages:Record<string,string>={
 archive_statistics_schema_required:'统计表尚未就绪，请升级并启动 Agent',
 archive_statistics_limit:'统计范围超过读取上限，请缩小日期范围或筛选用户、模型、渠道',
 archive_read_schema_mismatch:'归档表字段不兼容，请更新 Server',
 archive_read_table_missing:'所选月份的归档表不存在',
 archive_read_access_denied:'归档账号缺少查询权限',
 archive_read_timeout:'归档查询超时，请缩小筛选范围',
 archive_channel_column_missing:'该归档表没有渠道字段',
 archive_read_time_index_required:'归档月表缺少时间索引',
 archive_sealed_version_unavailable:'归档数据版本已变化，请重新查询',
 archive_identity_or_schema_mismatch:'归档库身份或版本不匹配',
 archive_readonly_permissions_required:'归档账号权限校验未通过',
 archive_read_row_too_large:'单条归档记录超过读取限制',
 archive_readonly_unavailable:'归档连接不可用，请检查连接设置',
}

export function archiveReadError(error:unknown):string{
 if(error instanceof ApiError)return `${messages[error.code]??'归档读取失败'}（${error.status} · ${error.code}）`
 return '归档读取失败，请重试'
}
