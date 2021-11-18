<?php
/**
 * 跑取快手数据示例
 * https://k.kuaishou.com/#/index
 */
namespace demo;
// 引入composer加载
require_once dirname(__FILE__)."/../vendor/autoload.php";
use \phpspiderman\phpspiderman;
$config = require_once "config.php";
$mysqldb = $config['db'];
$spider = new phpspiderman([
    //数据库配置
    "totalSumField" => "total",
    "totalPageField" => '',
    "PageField" => 'currentPage',
    "PageSize" => 20,
    "url" => 'https://k.kuaishou.com',
    "listpage" => "/rest/web/star/list",
    "cookie" => "",
    "params" => [
        'currentPage' => 1,
        'pageSize' => 20,
        'starOrderTag' => 3,
        'taskType' => 4
    ],
]);

$spider->handleListBefore = function($spider, $list)
{
    $list = \phpspiderman\content\json::decode($list);
    return $list;
};

$spider->handleList = function($spider, $params)
{
    $list = $spider->Http->getContent2($spider->config['listpage'], $params);
    $list = \phpspiderman\content\json::decode($list);
    return $list['starList'];
};

//获取到内容的处理
$spider->handleContent = function($spider, $val = null)
{
    if($val['userId'])
    {
        $data['userId'] = $val['userId'];
        $data['starId'] = $val['starId'];
        $data['name'] = $val['name'];
        $data['kwaiId'] = $val['kwaiId'];
        $data['gender'] = $val['gender']=='男'?'0':'1';
        $data['fansNumber'] = $val['fansNumber'];
        $data['areaTag'] = $val['areaTag'];
        $data['headUrl'] = $val['headUrl'];
    
        $data['liveQuotedPrice'] = $val['liveQuotedPrice'];
        $data['oneDaysOrderBid'] = $val['oneDaysOrderBid'];
        $data['threeDaysOrderBid'] = $val['threeDaysOrderBid'];
        $data['sevenDaysOrderBid'] = $val['sevenDaysOrderBid'];
    }else{
        $data = [];
    }
    return $data;
};
$spider->crawl();