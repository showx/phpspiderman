<?php
namespace phpspiderman;
use phpspiderman\tool\Http;

/**
 * 爬虫核心程序
 * Author:show
 */
Class phpspiderman
{
    //抓取配置 普通配置 多站点配置
    public $urlconfig = [
        'index_url' => '',
        'list' => '',
        'content' => '',
        //采集的是否443端口
        'ssl' => '443',
    ];
    //爬取类型
    public $typeArr = [
        1 => 'html',
        2 => 'json',
        3 => 'xml',
        4 => 'cms',
    ];
    public $proxyUrl = null;
    //爬取的类型
    public $type = 1;
    //抓取网站后缀 html
    public $suffix = '';
    // 获取列表页内容
    public $handleList = null;
    // 获取详情页内容
    public $handleContent = null;
    // 采集进程数
    public $worker_num = 4;
    public function __construct($config)
    {
        $this->urlconfig = $config;
        $this->type = $this->urlconfig['type'];
        if(isset($this->urlconfig['proxyUrl']))
        {
            $this->ProxyUrl = $this->urlconfig['proxyUrl'];
        }
        if(isset($this->urlconfig['worker_num']))
        {
            $this->worker_num = $this->urlconfig['worker_num'];
        }
        $this->Http = new Http;
    }

    /**
     * 检查需要的扩展
     * redis、pcntl、gmp、libevent
     */
    public function check()
    {
        if(!extension_loaded('posix') || !extension_loaded('pcntl') || !extension_loaded('libevent'))
        {
            //不使用libevent,更新比较小，使用event即可
            die('请安装必要的扩展[posix|pcntl|libevent]');
        }
    }

    /**
     * 开始爬取
     */
    public function crawl()
    {
        if($this->urlconfig['type'] != 2)
        {
            die("目前只支持api json采集!!");
        }
        //目前只有类型为type 2 ,即api json模式
        if($this->ProxyUrl)
        {
            $this->Http->setProxy($this->ProxyUrl);
        }
        if($this->urlconfig['urlport'])
        {
            $this->Http->port = $this->urlconfig['urlport'];
        }
        $this->Http->url2 = $this->urlconfig['url'];
        $header = $this->Http->setHeader($this->urlconfig['url'],$this->urlconfig['cookie']);
        $json_array = $this->urlconfig['body'];
        $list = [];
        \Co\run(function () use($header,&$list,$json_array) {
            $wg = new \Swoole\Coroutine\WaitGroup();
            $wg->add();
            go(function() use($header,&$list,$json_array){
                $list = $this->Http->getContent2($this->urlconfig['list'],$header,$json_array);
            });
            $wg->wait();
        });
        $list = \phpspiderman\content\json::decode($list);
        if($list)
        {
            if(!empty($this->urlconfig['totalPageField']))
            {
                $sum_page = $list[$this->urlconfig['totalPageField']];
            }else{
                $sum_page = round($list[$this->urlconfig['totalSumField']] / $this->urlconfig['PageSize']);
            }
            //从第一页开始采集
            $page = 1;
            $sum_page = 6; // 为了测试，这里设定一下
            while($page<=$sum_page)
            {
                $process = new \swoole_process(function() use($header,$page,$sum_page,$json_array){
                    echo "----------page {$page} / {$sum_page}------------".lr;
                    $json_array[$this->urlconfig['PageField']] = $page;
                    \Co\run(function () use($page,$header,$json_array) {
                        $wg = new \Swoole\Coroutine\WaitGroup();
                        $wg->add();
                        go(function() use($wg){
                            $this->pool = new \phpspiderman\tool\MysqlPool($this->urlconfig['mysqlconfig'],20);
                            $wg->done();
                        });
                        $wg->wait();
                        $wg->add();
                        go(function() use($page,$header,$json_array){
                            $mysql = $this->pool->get();
                            if($this->handleList)
                            {
                                $list = \call_user_func($this->handleList,$this,$header,$json_array);
                                foreach($list as $listData)
                                {
                                    if($this->handleContent)
                                    {
                                        $data = \call_user_func($this->handleContent,$listData);
                                        $arr_key = array_keys($data);
                                        $arr_value = array_values($data);
                                        $keyss = implode('`,`',$arr_key);
                                        $valuess = implode("','",$arr_value);
                                        //分号模式可能插入有问题
                                        $sql = "insert into {$this->urlconfig['table']} (`{$keyss}`) values('{$valuess}') ";
                                        $t = $mysql->query($sql);
                                        echo  $data['userId']."|{$page}|query return:".$t.lr;
                                    }
                                }
                            }
                            $this->pool->put($mysql);
                        });
                        $wg->wait();
                    });
                    
                },false,true);
                $process->name($this->urlconfig['table']."|".$page);
                $process->start();
                if($page % $this->worker_num == 0)
                {
                    //重新切换一下ip
                    $this->Http->transferIp();
                    echo '-----------------------------------------------------------------';
                    \swoole_process::wait();
                }
                $page++;
            }
            \swoole_process::wait();
        }
    }
}
