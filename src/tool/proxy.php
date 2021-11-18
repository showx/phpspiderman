<?php
/**
 * http网络请求类
 * Author:show
 */
namespace phpspiderman\tool;
class Proxy
{
    public static $i = 1;
    public static $proxy_url = "";
    public static $switchNum = 20;
    public static $proxyip;
    public static $proxyport;

    public static function setProxyIp($ip ,$port)
    {
        self::$proxyip = $ip;
        self::$proxyport = $port;
    }

    /**
     * 切换代理ip地址
     */
    public static function transferIp()
    {
        $ip_data = self::getProxy();
        if(!empty($ip_data))
        {
            echo "TransferIP:{$ip_data['0']}:{$ip_data['1']}".PHP_EOL;
            self::$proxyip = $ip_data['0'];
            self::$proxyport = $ip_data['1'];
        }
    }

    /**
     * 设置代理地址
     */
    public static function setProxy($url)
    {
        self::$proxy_url = $url;
    }

    /**
     * 获取代理服务器的ip
     */
    public static function getProxy()
    {
        if(self::$proxy_url)
        {
            self::$i = self::$i + 1;
            $tmp = self::$i % self::$switchNum;
            if($tmp === 0 || self::$i==2)
            {
                $data = file_get_contents(self::$proxy_url);
                $data = trim($data);
                $data2 = json_decode($data, true);
                if($data2)
                {
                    return [];
                }
                $tmp = explode(":", $data);
            }else{
                return [];
            }
            
        }else{
            $tmp = [];
        }
        return $tmp;
    }



}