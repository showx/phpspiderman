<?php
/**
 * 爬虫相关配置
 */
namespace phpsiderman;

return [
    //mysql配置
    'db' => [
        'host' => '127.0.0.1',
        'port' => '3306',
        'username' => 'root',
        'password' => 'root',
    ],
    'redis' => [
        'host' => '127.0.0.1',
        'port' => '6379',
        'auth' => '',
    ],
    //采集进程数
    'worker' => 4,
    'phpspider_version' => '1.0',
    'debug' => '1',
];