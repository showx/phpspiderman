<?php
namespace phpspiderman;
use phpspiderman\tool\http;

/**
 * 爬虫核心程序
 * Author:show
 */
Class phpspiderman
{
    // 获取列表页内容
    public $handleList = null;
    public $handleListBefore = null;
    // 获取详情页内容
    public $handleContent = null;
    public $handlefinish = null;
    // 获取详情页所需要的其它内容
    public $handleContentDetail = null;
    public function __construct($config)
    {
        $this->config = $config;
        $this->http = new http;
    }

    /**
     * 检查需要的扩展
     */
    public function check()
    {
        if(!extension_loaded('swoole'))
        {
            //不使用libevent,更新比较小，使用event即可
            die('请安装必要的扩展[swoole]');
        }
        if(PHP_SAPI != 'cli')
        {
            die('只能在php cli的环境下执行');
        }
    }

    public function mysqlinitpool()
    {
        $wg = new \Swoole\Coroutine\WaitGroup();
        $wg->add();
        go(function() use($wg){
            $this->pool = new \phpspiderman\tool\MysqlPool($this->config['mysqlconfig'], 20);
            $wg->done();
        });
        $wg->wait();
    }
    // public function handleContent(){}
    // public function handlefinish($spider){ echo '[采集完毕]';}
    /**
     * 开始爬取
     */
    public function crawl()
    {
        $this->check();
        \Co\run(function () {
            $this->http->setUrl($this->config['url']);
            $params = !empty($this->config['params']) ? $this->config['params'] : $this->config['raw'];
            $list = [];
            $wg = new \Swoole\Coroutine\WaitGroup();
            $wg->add();
            go(function() use(&$list, $params, $wg){
                $list = $this->http->getContent($this->config['listpage'], 'post', $params);
                $wg->done();
            });
            $wg->wait();
            $list = \call_user_func($this->handleListBefore, $this, $list);
            if($list)
            {
                $page = 1;
                $sum_page = 0;
                if(!empty($this->config['totalPageField']))
                {
                    $sum_page = $this->config['totalPageField'];
                    if($sum_page == '1')
                    {
                        // 自动判断切换ip
                        $list = \call_user_func($this->handleList, $this, $params);
                        foreach($list as $listData)
                        {
                            if($this->handleContent)
                            {
                                \call_user_func($this->handleContent, $listData);
                            }
                        }
                    }
                }else{
                    if(isset($list[$this->config['totalSumField']]) && isset($this->config['PageSize']))
                    {
                        $sum_page = round($list[$this->config['totalSumField']] / $this->config['PageSize']);
                    }
                }
                // $sum_page = 10000; // 为了测试，这里设定一下
                while($page <= $sum_page)
                {
                        echo "----------page {$page} / {$sum_page}------------".lr;
                        $json_array[$this->config['PageField']] = $page;
                            $wg = new \Swoole\Coroutine\WaitGroup();
                            $wg->add();
                            go(function() use($page, $params, $wg){
                                $mysql = $this->pool->get();
                                if($this->handleList)
                                {
                                    $list = \call_user_func($this->handleList, $this, $params);
                                    foreach($list as $listData)
                                    {
                                        if($this->handleContent)
                                        {
                                            \call_user_func($this->handleContent, $this, $listData);
                                        }
                                    }
                                }
                                $this->pool->put($mysql);
                                $wg->done();
                            });
                            $wg->wait();
                    
                        $this->Http->transferIp();
                    $page++;
                }
            }
            if($this->handlefinish)
            {
                \call_user_func($this->handeFinish, $this);
            }
            echo '[phpspiderman结束]';
        });
    }
}
