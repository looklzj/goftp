package controllers

import (
 "anime/models"

 "github.com/astaxie/beego/orm"
)

type HomeController struct {
	BaseController
}

func (c *HomeController)Home()  {
	c.TplName="index.html"
	tag:=c.GetString("tag")

	animes:=[]*models.Anime{}
	qs:=orm.NewOrm().QueryTable("anime").RelatedSel("tag").Limit(20,0)
	if tag!=""{
		qs.Filter("tag__Name",tag).All(&animes)
	} else {
		qs.All(&animes)
	}

	c.Data["animes"]=animes
}
