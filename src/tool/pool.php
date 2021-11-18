<?php
namespace phpspiderman\tool;

/**
 * mysql协程连接池
 * 基于swoole
 * Author:show
 */
class Pool
{
    /**
     * @var \Swoole\Coroutine\Channel
     */
    protected $pool = ['mysql' => [], 'redis' => []];

    /**
     * MysqlPool constructor.
     * @param int $size 连接池的尺寸
     */
    function __construct($type = 'mysql', $config, $size = 20)
    {
        $this->pool[$type] = new \Swoole\Coroutine\Channel($size);
        for ($i = 0; $i < $size; $i++)
        {
            if($type == 'mysql')
            {
                $mysql = new \Swoole\Coroutine\Mysql();
                $mysql_config = [
                    'host' => $config['host'],
                    'port' => $config['port'],
                    'user' => $config['username'],
                    'password' => $config['password'],
                    'database' => $config['dbname'],
                    'charset' => 'utf8mb4'
                ];
                $res = $mysql->connect($mysql_config);
            }elseif($type == 'redis')
            {
                $redis = new \Swoole\Coroutine\Redis();
                $res = $redis->connect($config['host'], $config['port']);
            }
            if ($res == false)
            {
                throw new \RuntimeException("failed to connect mysql server.");
            }
            else
            {
                $this->put($type, $res);
            }
        }
    }

    function put($type, $mysql)
    {
        if(in_array($type, ['mysql', 'redis']))
        {
            $this->pool[$type]->push($mysql);
        }
        
    }

    function get($type)
    {
        return $this->pool[$type]->pop();
    }
}

