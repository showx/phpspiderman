<?php
namespace phpsiderman;
use Workerman\Worker;
/**
 * 爬虫核心程序
 * Author:show
 */
Class phpspiderman
{
    //抓取配置，普通配置 多站点配置
    public $urlconfig = [
        'index_url' => '',
        'list' => '',
        'content' => '',
    ];
    public function __construct($config)
    {
        $this->urlconfig = $config;
    }
    public $type_arr = [
        '1' => 'html',
        '2' => 'json',
        '3' => 'xml',
        //特殊类型
        '4' => 'cms',
    ];
    //爬取的类型
    public $type = '1';
    //抓取网站后缀 html
    public $suffix = '';
    /**
     * 检查需要的扩展
     * redis、pcntl、gmp
     */
    public function check()
    {
        if(!extension_loaded('posix') || !extension_loaded('pcntl') || !extension_loaded('event'))
        {
            //不使用libevent,更新比较小，使用event即可
            die('请安装必要的扩展[posix|pcntl|event]');
        }
    }

    /**
     * 开始爬取
     */
    public function crawl()
    {

    }

}
