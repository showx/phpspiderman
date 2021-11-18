<?php
/**
 * 星图采集示例
 * start.toutiao.com
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
    "url" => 'https://star.toutiao.com',
    "list" => "/rest/web/star/list",
]);

$spider->handleList = function($spider, $param)
{
    $list = $spider->Http->getContent($spider->config['list'], 'post', $param);
    $list = \phpspiderman\content\json::decode($list);
    return $list['starList'];
};
//获取到内容的处理
$spider->handleContent = function($val = null)
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
    return $data;
};
$spider->crawl();
