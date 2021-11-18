<?php
/**
 * 爬虫相关配置
 */
namespace phpsiderman;

return [
    //mysql配置
    'db' => [
        'host' => '172.17.0.5',
        'dbname' => 'phpshow',
        'port' => '3306',
        'username' => 'root',
        'password' => 'root',
    ],
    'redis' => [
        'host' => '127.0.0.1',
        'port' => '6379',
        'auth' => '',
    ],
];